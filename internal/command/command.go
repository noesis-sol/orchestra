// Package command runs the external commands orchestra drives (git, bd, herdr, claude) and
// reports their failures with stderr and a short form of the arguments.
package command

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Output runs a command in dir and returns its stdout. The error carries stderr, so callers can
// log it.
func Output(dir, name string, args ...string) (string, error) {
	return OutputContext(context.Background(), dir, name, args...)
}

// OutputContext is Output, stopped when ctx is cancelled.
func OutputContext(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "BD_JSON_ENVELOPE=0") // pin the bd --json shape
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stdout.String(), fmt.Errorf("%s %s: %w: %s", name, shortArgs(args), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// shortArgs renders arguments for an error message, cutting long ones (a whole prompt, say).
func shortArgs(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		a = strings.ReplaceAll(a, "\n", " ")
		if r := []rune(a); len(r) > 60 {
			a = string(r[:60]) + "…"
		}
		out[i] = a
	}
	return strings.Join(out, " ")
}
