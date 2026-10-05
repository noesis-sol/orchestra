//go:build unix

package command

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// hang is a command that never finishes by itself. The shell runs sleep as its child rather than
// in its place, so sleep goes on holding the output after the shell is stopped, as a git hook can.
// It first writes its PID, its group's ID, to the file pgid, for unhung.
var hang = []string{"-c", "echo $$ > pgid; sleep 600; echo late"}

// hung is how long these tests wait for what takes a moment (a command started, stopped or finished, a
// killed process gone) before taking it for stuck: far longer than that takes under the race detector on
// a machine loaded by other workers' tests, and far shorter than the 10 minutes their commands sleep, so
// a slow run passes and only one that waits for such a command reaches it.
const hung = time.Minute

// unhung returns what call returns, unless call is still running after hung: then it kills the process
// group whose ID the command wrote to the file pgid in dir, so that call returns, and fails the test,
// which wanted call back as want says.
func unhung[T any](t *testing.T, dir, want string, call func() (T, error)) (T, error) {
	t.Helper()
	type result struct {
		v   T
		err error
	}
	done := make(chan result, 1)
	go func() {
		v, err := call()
		done <- result{v, err}
	}()
	var r result
	select {
	case r = <-done:
	case <-time.After(hung):
		killGroup(dir)
		r = <-done
		t.Fatalf("still running after %s, want it back %s", hung, want)
	}
	return r.v, r.err
}

// killGroup kills the process group whose ID a command wrote to the file pgid in dir, if it wrote one.
func killGroup(dir string) {
	b, err := os.ReadFile(filepath.Join(dir, "pgid"))
	if err != nil {
		return // the command never got that far
	}
	// Above 1: a group ID of 0 or 1 would have kill reach the test's own group or every process.
	if pgid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && pgid > 1 {
		_ = syscall.Kill(-pgid, syscall.SIGKILL) // best effort: the group may have gone
	}
}

// Ctrl+C stops a hung command at once, and the error says why it stopped.
func TestOutputStopsAHungCommandWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancelCause(context.Background())
	time.AfterFunc(100*time.Millisecond, func() { cancel(errors.New("stopped with Ctrl+C")) })
	_, err := unhung(t, dir, "once cancelled", func() (string, error) {
		return Output(ctx, ReadLimit, dir, "sh", hang...)
	})
	if err == nil || err.Error() != "sh -c echo $$ > pgid; sleep 600; echo late: stopped with Ctrl+C" {
		t.Errorf("error %v, want it to name the command and why it stopped", err)
	}
}

// A hung command is stopped at its time limit, and the error names the limit.
func TestOutputStopsAHungCommandAtItsLimit(t *testing.T) {
	dir := t.TempDir()
	_, err := unhung(t, dir, "at its 200ms time limit", func() (string, error) {
		return Output(context.Background(), 200*time.Millisecond, dir, "sh", hang...)
	})
	if err == nil || err.Error() != "sh -c echo $$ > pgid; sleep 600; echo late: timed out after 200ms" {
		t.Errorf("error %v, want it to name the command and the limit", err)
	}
}

// A stopped command's stderr stays in the error.
func TestOutputStoppedKeepsStderr(t *testing.T) {
	_, err := Output(context.Background(), 200*time.Millisecond, "", "sh", "-c", "echo waiting on the lock >&2; sleep 5; echo late")
	if err == nil || !strings.HasSuffix(err.Error(), ": timed out after 200ms (waiting on the lock)") {
		t.Errorf("error %v, want the limit and stderr", err)
	}
}

// A command that succeeds but leaves a process behind holding its output succeeds, without waiting
// for that process.
func TestOutputDoesNotWaitForALeftoverHoldingTheOutput(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() { killGroup(dir) }) // the leftover would sleep on past the test
	out, err := unhung(t, dir, "once the command exited, with its leftover still running", func() (string, error) {
		return Output(context.Background(), ReadLimit, dir, "sh", "-c", "echo $$ > pgid; sleep 600 & echo ok")
	})
	if err != nil || out != "ok\n" {
		t.Errorf("got %q, %v", out, err)
	}
}

// A command's own failure is reported as before: its error and stderr, and no limit.
func TestOutputReportsAFailure(t *testing.T) {
	_, err := Output(context.Background(), ReadLimit, "", "sh", "-c", "echo locked >&2; exit 3")
	if err == nil || err.Error() != "sh -c echo locked >&2; exit 3: exit status 3: locked" {
		t.Errorf("error %v", err)
	}
}

// A failure is an *Error with the command, its stderr and exec's error underneath.
func TestOutputFailureIsTyped(t *testing.T) {
	_, err := Output(context.Background(), ReadLimit, "", "sh", "-c", "echo locked >&2; exit 3")
	var e *Error
	if !errors.As(err, &e) || e.Name != "sh" || e.Stderr != "locked" || e.Stopped {
		t.Fatalf("error %#v", err)
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 3 {
		t.Errorf("exec's error should be underneath: %v", err)
	}
	_, err = Output(context.Background(), 200*time.Millisecond, t.TempDir(), "sh", hang...)
	if !errors.As(err, &e) || !e.Stopped || e.Err.Error() != "timed out after 200ms" {
		t.Errorf("stopped command: %#v", err)
	}
}

// A command runs in a process group of its own, so a Ctrl+C typed at the terminal, which goes to
// orchestra's group, doesn't reach it.
func TestOutputRunsTheCommandInItsOwnProcessGroup(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := Output(ctx, ReadLimit, dir, "sh", "-c", "echo $$ > pid.tmp; mv pid.tmp pid; sleep 30")
		done <- err
	}()
	defer func() {
		cancel()
		<-done // stopped by the cancel; the test is about its group
	}()
	var pid int
	for start := time.Now(); pid == 0; time.Sleep(10 * time.Millisecond) {
		if b, err := os.ReadFile(filepath.Join(dir, "pid")); err == nil {
			if pid, err = strconv.Atoi(strings.TrimSpace(string(b))); err != nil {
				t.Fatal(err)
			}
		} else if time.Since(start) > hung {
			t.Fatal("the command didn't start")
		}
	}
	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		t.Fatal(err)
	}
	if pgid == syscall.Getpgrp() || pgid != pid {
		t.Errorf("the command (pid %d) is in process group %d, the caller in %d; want a group of its own",
			pid, pgid, syscall.Getpgrp())
	}
}
