// Package command runs the external commands orchestra drives (git, bd, herdr, claude) and
// reports their failures with stderr and a short form of the arguments.
package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Time limits on the commands orchestra runs, by kind. A command still running at its limit is
// stopped and fails, so a hung bd, git or herdr can't hold a worker forever. Waits that have a
// time limit of their own (herdr agent wait, prompt --wait) are given that one, with ReadLimit to
// spare.
const (
	ReadLimit  = 30 * time.Second // a read, and any herdr call
	WriteLimit = 2 * time.Minute  // a write: git worktrees, rebases, merges and branches, bd updates
)

// stopGrace is how long a command that is stopped has between SIGTERM and SIGKILL (git removes its
// lock files on SIGTERM, not on SIGKILL), and how long Output then waits for output pipes that a
// process the command started still holds.
const stopGrace = 500 * time.Millisecond

// Output runs a command in dir and returns its stdout. The command is stopped when ctx is done or
// once it has run for limit (0 for no limit). The error is an *Error carrying stderr, and for a
// command that was stopped why: the limit it ran into, or the cause ctx was cancelled with. The
// command runs in its own process group, so a Ctrl+C at the terminal, or a SIGHUP from closing it,
// reaches orchestra alone: a merge it has under way isn't killed halfway.
func Output(ctx context.Context, limit time.Duration, dir, name string, args ...string) (string, error) {
	return run(ctx, limit, dir, nil, name, args)
}

// OutputWithInput runs a command as Output does, with input on its stdin.
func OutputWithInput(ctx context.Context, limit time.Duration, dir, input, name string,
	args ...string) (string, error) {
	return run(ctx, limit, dir, strings.NewReader(input), name, args)
}

// run runs a command for Output and OutputWithInput, its stdin read from stdin (nil for none).
func run(ctx context.Context, limit time.Duration, dir string, stdin io.Reader, name string,
	args []string) (string, error) {
	if limit > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, limit, timedOut(limit))
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "BD_JSON_ENVELOPE=0") // pin the bd --json shape
	ownGroup(cmd)
	cmd.Cancel = func() error { return terminate(cmd.Process) }
	cmd.WaitDelay = stopGrace
	var stdout, stderr bytes.Buffer
	cmd.Stdin = stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if errors.Is(err, exec.ErrWaitDelay) && ctx.Err() == nil {
		err = nil // exited 0, leaving a process behind that holds the output
	}
	if err == nil {
		return stdout.String(), nil
	}
	e := &Error{Name: name, Args: args, Err: err, Stderr: strings.TrimSpace(stderr.String())}
	if cause := context.Cause(ctx); cause != nil {
		e.Err, e.Stopped = cause, true
	}
	return stdout.String(), e
}

// timedOut is why a command was stopped at its time limit: it reads "timed out after 2m", and
// errors.Is takes it for context.DeadlineExceeded.
type timedOut time.Duration

func (t timedOut) Error() string { return "timed out after " + shortDuration(time.Duration(t)) }

func (t timedOut) Unwrap() error { return context.DeadlineExceeded }

// Error is a command that failed: what ran, why it failed, and what it said on stderr. Callers that
// react to a particular failure read its fields (or Err, through errors.As) instead of its text.
type Error struct {
	Name    string
	Args    []string
	Err     error  // how the command failed (*exec.ExitError, say), or why it was stopped
	Stderr  string // trimmed
	Stopped bool   // stopped by its time limit or a cancelled context, Err saying which
}

// Error reads "git rebase main: exit status 1: <stderr>", or for a stopped command
// "git rebase main: timed out after 2m (<stderr>)". Without Args it names the command alone.
func (e *Error) Error() string {
	what := e.Name
	if len(e.Args) > 0 {
		what += " " + shortArgs(e.Args)
	}
	switch {
	case !e.Stopped:
		return fmt.Sprintf("%s: %v: %s", what, e.Err, e.Stderr)
	case e.Stderr != "":
		return fmt.Sprintf("%s: %v (%s)", what, e.Err, e.Stderr)
	}
	return fmt.Sprintf("%s: %v", what, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// shortDuration is d as a person would write it: 2m, 30s, 500ms.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = s[:len(s)-2]
	}
	if strings.HasSuffix(s, "h0m") {
		s = s[:len(s)-2]
	}
	return s
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
