// Package command runs the external commands orchestra drives (git, bd, herdr, claude) and
// reports their failures with stderr and a short form of the arguments.
package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
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

// GroupOutput runs a command in dir in its own process group and returns its combined stdout
// and stderr. When ctx is done the whole group is stopped, not just the command: SIGTERM, then
// SIGKILL after grace. Anything the command left running in its group is killed as soon as it
// exits, and Wait gives up on output pipes still held open after twice grace.
func GroupOutput(ctx context.Context, grace time.Duration, dir, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	g := inGroup(cmd, grace)
	cmd.WaitDelay = 2 * grace
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	g.wait()
	err := cmd.Wait()
	g.stop()
	if err == nil || errors.Is(err, exec.ErrWaitDelay) { // exited 0, though maybe leaving a process behind
		err = ctx.Err()
	}
	return out.Bytes(), err
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

// ShellQuote quotes s as one word for a POSIX shell.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
