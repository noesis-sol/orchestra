// Package command runs the external commands orchestra drives (git, bd, herdr, claude) and
// reports their failures with stderr and a short form of the arguments. Interactive runs one on
// the terminal instead, for the user to work in.
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

// stopGrace is how long a command that is stopped, and whatever it started, has between SIGTERM and
// SIGKILL (git removes its lock files on SIGTERM, not on SIGKILL), and how long Output waits for
// output pipes that a process the command started still holds.
const stopGrace = 500 * time.Millisecond

// Output runs a command in dir and returns its stdout. The command is stopped when ctx is done or
// once it has run for limit (0 for no limit). The error is an *Error carrying stderr, and for a
// command that was stopped why: the limit it ran into, or the cause ctx was cancelled with. The
// command runs in its own process group, so a Ctrl+C at the terminal, or a SIGHUP from closing it,
// reaches orchestra alone: a merge it has under way isn't killed halfway. Stopping the command stops
// that whole group, whatever the command started in it (git's hooks, its ssh): SIGTERM, then SIGKILL
// stopGrace later. A command that exits by itself may leave a process running, such as a server bd
// starts, and Output leaves it running, without waiting for the output pipes it holds.
func Output(ctx context.Context, limit time.Duration, dir, name string, args ...string) (string, error) {
	return run(ctx, limit, dir, nil, nil, name, args)
}

// OutputWithInput runs a command as Output does, with input on its stdin and env (NAME=value each)
// added to its environment, where it overrides a variable of the same name.
func OutputWithInput(ctx context.Context, limit time.Duration, dir string, env []string, input, name string,
	args ...string) (string, error) {
	return run(ctx, limit, dir, env, strings.NewReader(input), name, args)
}

// run runs a command for Output and OutputWithInput, with env added to its environment and its
// stdin read from stdin (nil for none).
func run(ctx context.Context, limit time.Duration, dir string, env []string, stdin io.Reader, name string,
	args []string) (string, error) {
	if limit > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, limit, timedOut(limit))
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	// BD_JSON_ENVELOPE pins the bd --json shape. Of two entries with one name, the last wins.
	cmd.Env = append(append(os.Environ(), "BD_JSON_ENVELOPE=0"), env...)
	g := inGroup(cmd, stopGrace)
	cmd.WaitDelay = stopGrace
	var stdout, stderr bytes.Buffer
	cmd.Stdin = stdin
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Start()
	if err == nil {
		g.settle()
		err = cmd.Wait()
		g.release()
	}
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

func (t timedOut) Error() string { return "timed out after " + ShortDuration(time.Duration(t)) }

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

// ShortDuration is d as a person would write it: 2h, 1h30m, 45m, 1m30s, 500ms.
func ShortDuration(d time.Duration) string {
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

// interactiveGrace is how long a command on the terminal (Interactive) has between SIGTERM and
// SIGKILL: time to put the terminal back as it found it, out of raw mode.
const interactiveGrace = 5 * time.Second

// Interactive runs a command in dir on the terminal and waits, without a time limit, for it to
// exit: a program the user works in, such as an interactive Claude Code session. It reads stdin and
// writes stdout and stderr itself, the terminal's own files when given orchestra's. Unlike Output's
// commands it stays in orchestra's process group: a program in another group can't read the
// terminal. So Ctrl+C at the terminal reaches it, and orchestra too, which must leave it to the
// command. Ctrl+Z, which a program in raw mode such as Claude Code takes to stop itself alone,
// suspends orchestra with it, so that the shell gets the terminal back, and fg brings both back
// (runOnTerminal). When ctx is done the command gets SIGTERM, and SIGKILL if it is still running
// interactiveGrace later. The error is an *Error, without Stderr: the command wrote that to the
// terminal.
func Interactive(ctx context.Context, dir string, stdin io.Reader, stdout, stderr io.Writer, name string,
	args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	cmd.Cancel = func() error { return terminate(cmd.Process) }
	cmd.WaitDelay = interactiveGrace
	err := runOnTerminal(cmd)
	if errors.Is(err, exec.ErrWaitDelay) && ctx.Err() == nil {
		err = nil // exited 0, leaving a process behind that holds a stream that isn't a file
	}
	if err == nil {
		return nil
	}
	e := &Error{Name: name, Args: args, Err: err}
	if cause := context.Cause(ctx); cause != nil {
		e.Err, e.Stopped = cause, true
	}
	return e
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
