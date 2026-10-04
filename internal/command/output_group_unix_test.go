//go:build unix

package command

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// startsChildren is a command that starts two children and waits for them: one that ends on
// SIGTERM, as a git hook would, and one that ignores it, so that only a SIGKILL sent after the
// command itself has ended on the SIGTERM stops it. Once both have started, the second writes the
// command's PID, its group's ID, to the file pgid.
const startsChildren = `sleep 30 & (trap "" TERM; echo $$ > pgid.tmp; mv pgid.tmp pgid; exec sleep 30) & wait`

// awaitFile waits for a command to write file, once it has started.
func awaitFile(t *testing.T, file string) {
	t.Helper()
	for start := time.Now(); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(file); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Since(start) > soon {
			t.Fatalf("the command didn't write %s", filepath.Base(file))
		}
	}
}

// outputCancelled runs script with Output in dir, cancels it once it has written dir/pgid, and
// returns the group's ID and Output's error.
func outputCancelled(t *testing.T, dir, script string) (int, error) {
	t.Helper()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	done := make(chan error, 1)
	go func() {
		_, err := Output(ctx, ReadLimit, dir, "sh", "-c", script)
		done <- err
	}()
	awaitFile(t, filepath.Join(dir, "pgid"))
	pgid := childPID(t, filepath.Join(dir, "pgid"))
	cancel(errors.New("stopped with Ctrl+C"))
	select {
	case err := <-done:
		return pgid, err
	case <-time.After(soon):
		_ = syscall.Kill(-pgid, syscall.SIGKILL) // best effort: the test already fails
		t.Fatalf("Output still runs %s after the cancel", soon)
	}
	return 0, nil
}

// assertGroupGone fails the test if a process of group pgid still runs.
func assertGroupGone(t *testing.T, pgid int) {
	t.Helper()
	if alive(-pgid) {
		_ = syscall.Kill(-pgid, syscall.SIGKILL) // best effort: the test already fails
		t.Errorf("a process of the command's group %d still runs", pgid)
	}
}

// A command stopped at its time limit takes with it everything it started, even a child that
// outlives the SIGTERM that ended the command.
func TestOutputStopsTheCommandsWholeGroupAtItsLimit(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	_, err := Output(context.Background(), time.Second, dir, "sh", "-c", startsChildren)
	if took := time.Since(start); took > soon {
		t.Errorf("returned after %s", took)
	}
	var e *Error
	if !errors.As(err, &e) || !e.Stopped || e.Err.Error() != "timed out after 1s" {
		t.Errorf("error %v, want the command stopped at its limit", err)
	}
	assertGroupGone(t, childPID(t, filepath.Join(dir, "pgid")))
}

// A command stopped by a cancelled context, Ctrl+C say, takes with it everything it started.
func TestOutputStopsTheCommandsWholeGroupWhenCancelled(t *testing.T) {
	dir := t.TempDir()
	pgid, err := outputCancelled(t, dir, startsChildren)
	var e *Error
	if !errors.As(err, &e) || !e.Stopped || e.Err.Error() != "stopped with Ctrl+C" {
		t.Errorf("error %v, want the command stopped by the cancel", err)
	}
	assertGroupGone(t, pgid)
}

// Everything a stopped command started gets SIGTERM first, as git does, and the grace that follows
// it: a child still cleaning up after the command itself has ended isn't killed halfway.
func TestOutputGivesTheCommandsGroupSIGTERMAndItsGrace(t *testing.T) {
	dir := t.TempDir()
	script := `trap "echo > leader; exit 1" TERM
(trap "sleep 0.1; echo > child; exit 1" TERM; echo $$ > pgid.tmp; mv pgid.tmp pgid; sleep 30 & wait) &
wait`
	pgid, _ := outputCancelled(t, dir, script) // stopped by the cancel; the test is about the signals
	for _, f := range []string{"leader", "child"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("the %s didn't clean up on SIGTERM: %v", f, err)
		}
	}
	assertGroupGone(t, pgid)
}

// A command that succeeds and leaves a process behind holding its output returns that output, and
// the process goes on running: bd may start a server on purpose.
func TestOutputLeavesRunningWhatASucceedingCommandLeft(t *testing.T) {
	dir := t.TempDir()
	out, err := Output(context.Background(), ReadLimit, dir, "sh", "-c", `(sleep 1; echo > late) & echo ok`)
	if err != nil || strings.TrimSpace(out) != "ok" {
		t.Errorf("got %q, %v", out, err)
	}
	awaitFile(t, filepath.Join(dir, "late")) // written after Output returned, so not killed
}
