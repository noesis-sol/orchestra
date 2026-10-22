package command

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// envKey is the context key of what WithEnv adds to the commands' environment.
type envKey struct{}

// WithEnv returns ctx with env (NAME=value each) added to the environment of every command run with
// it, or with a context made from it, over a variable of the same name in orchestra's own or in what
// an earlier WithEnv added. A command's name is looked up in the PATH of that environment, not
// orchestra's: so a test gives the run it starts an environment of its own, with its fake tools first
// on its PATH, without setting the process's, which the tests running in parallel share.
func WithEnv(ctx context.Context, env ...string) context.Context {
	return context.WithValue(ctx, envKey{}, append(slices.Clip(added(ctx)), env...))
}

// WithEnvOf returns ctx with what WithEnv added to from added to it too: for a context that doesn't
// come from from, such as one a library makes from context.Background.
func WithEnvOf(ctx, from context.Context) context.Context { return WithEnv(ctx, added(from)...) }

// added is what WithEnv added to the environment of the commands run with ctx.
func added(ctx context.Context) []string {
	env, _ := ctx.Value(envKey{}).([]string)
	return env
}

// Environ is the environment of the commands run with ctx: orchestra's, with what WithEnv added.
func Environ(ctx context.Context) []string {
	return append(os.Environ(), added(ctx)...) // of two entries with one name, the last wins
}

// Getenv is the variable name in the environment of the commands run with ctx (Environ), "" if it
// isn't set.
func Getenv(ctx context.Context, name string) string {
	if v, ok := lookupAdded(ctx, name); ok {
		return v
	}
	return os.Getenv(name)
}

// lookupAdded is the variable name as WithEnv last added it to ctx, if it did.
func lookupAdded(ctx context.Context, name string) (string, bool) {
	for _, kv := range slices.Backward(added(ctx)) {
		if k, v, _ := strings.Cut(kv, "="); k == name {
			return v, true
		}
	}
	return "", false
}

// LookPath is exec.LookPath, looking in the PATH of the commands run with ctx (see WithEnv).
func LookPath(ctx context.Context, name string) (string, error) {
	path, ok := lookupAdded(ctx, "PATH")
	if !ok || filepath.Base(name) != name {
		return exec.LookPath(name)
	}
	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue // as exec.LookPath, which runs nothing it finds by the working directory (exec.ErrDot)
		}
		if p, err := exec.LookPath(filepath.Join(dir, name)); err == nil {
			return p, nil
		}
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

// unset returns env without its entries that are a name alone, and without each variable such an
// entry names that an entry before it sets: so NAME leaves out NAME, and NAME=value after it sets it
// again.
func unset(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.Contains(kv, "=") {
			kept = append(kept, kv)
			continue
		}
		kept = slices.DeleteFunc(kept, func(e string) bool { k, _, _ := strings.Cut(e, "="); return k == kv })
	}
	return kept
}

// newCmd is exec.CommandContext with the environment of the commands run with ctx (Environ), env
// added to it, and name looked up in its PATH (LookPath). An entry of env that is a name alone, with
// no =, leaves that variable out of the environment (see unset).
func newCmd(ctx context.Context, env []string, name string, args []string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	if _, ok := lookupAdded(ctx, "PATH"); ok {
		if p, err := LookPath(ctx, name); err != nil {
			cmd.Path, cmd.Err = name, err
		} else {
			cmd.Path, cmd.Err = p, nil
		}
	}
	if len(added(ctx)) > 0 || env != nil {
		cmd.Env = unset(append(Environ(ctx), env...))
	} // else nil: exec gives the command orchestra's environment, with PWD set to its Dir
	return cmd
}
