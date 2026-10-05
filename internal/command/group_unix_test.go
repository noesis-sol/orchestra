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

// alive reports whether pid still runs, allowing a killed process until hung to be reaped.
func alive(pid int) bool {
	for start := time.Now(); time.Since(start) < hung; time.Sleep(100 * time.Millisecond) {
		if errors.Is(syscall.Kill(pid, syscall.Signal(0)), syscall.ESRCH) {
			return false
		}
	}
	return true
}

// childPID reads the PID a test command wrote to file.
func childPID(t *testing.T, file string) int {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func TestGroupOutputStopsAChildThatHoldsTheOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: waits out a time limit and its grace")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	// The child ignores SIGTERM and keeps the output pipe open, so only the group's SIGKILL ends it.
	_, err := unhung(t, dir, "within the timeout plus grace", func() ([]byte, error) {
		return GroupOutput(ctx, time.Second, dir, "sh", "-c", `trap "" TERM; echo $$ > pgid; sleep 600 & wait`)
	})
	if err == nil {
		t.Error("a timed-out command should fail")
	}
	assertGroupGone(t, childPID(t, filepath.Join(dir, "pgid")))
}

func TestGroupOutputPassesAndCleansUpAfterABackgroundChild(t *testing.T) {
	dir := t.TempDir()
	out, err := GroupOutput(context.Background(), 200*time.Millisecond, dir, "sh", "-c",
		`echo $$ > pgid; sleep 600 > /dev/null & echo ok`)
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Errorf("got %q, %v", out, err)
	}
	assertGroupGone(t, childPID(t, filepath.Join(dir, "pgid")))
}

func TestGroupOutputReportsFailure(t *testing.T) {
	if _, err := GroupOutput(context.Background(), time.Second, t.TempDir(), "sh", "-c", "exit 3"); err == nil {
		t.Error("a failing command should fail")
	}
}

func TestGroupOutputKillsALeftoverHoldingTheOutputOnceTheCommandExits(t *testing.T) {
	dir := t.TempDir()
	// With a grace of hung, Wait gives up on the leftover's output only after twice that, so a
	// GroupOutput that leaves the leftover running until then is still running when unhung gives up.
	out, err := unhung(t, dir, "once the command exited", func() ([]byte, error) {
		return GroupOutput(context.Background(), hung, dir, "sh", "-c", `echo $$ > pgid; sleep 600 & echo ok`)
	})
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Errorf("got %q, %v", out, err)
	}
	assertGroupGone(t, childPID(t, filepath.Join(dir, "pgid")))
}

// awaitExitWithin runs awaitExit on cmd's process, failing if it takes longer than d.
func awaitExitWithin(t *testing.T, cmd *exec.Cmd, d time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- awaitExit(cmd.Process.Pid) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(d):
		_ = cmd.Process.Kill() // best effort: the test already fails
		t.Fatalf("awaitExit still waits after %s", d)
	}
}

func TestAwaitExitLeavesTheCommandUnreaped(t *testing.T) {
	cmd := exec.Command("sh", "-c", "sleep 0.2; exit 3")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	awaitExitWithin(t, cmd, hung)
	if err := cmd.Wait(); cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 3 {
		t.Errorf("Wait after awaitExit: %v, want exit status 3", err)
	}
}

func TestAwaitExitReturnsForACommandThatAlreadyExited(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // a zombie by now
	awaitExitWithin(t, cmd, hung)
	if err := cmd.Wait(); err != nil {
		t.Errorf("Wait after awaitExit: %v", err)
	}
}

func TestCancelSignalsNothingOnceTheCommandHasExited(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "true")
	g := inGroup(cmd, time.Second)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	g.wait()
	if err := cmd.Cancel(); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("Cancel after the command exited: %v, want os.ErrProcessDone", err)
	}
	if g.kill != nil {
		t.Error("Cancel armed a SIGKILL after the command exited")
	}
	_ = cmd.Wait() // only reaps it; the test is about Cancel
}

func TestTheSIGKILLStopsOnceTheCommandExits(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sleep", "30")
	g := inGroup(cmd, time.Hour)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Cancel(); err != nil {
		t.Fatal(err)
	}
	g.wait() // the SIGTERM ends sleep
	if g.kill.Stop() {
		t.Error("the SIGKILL was still armed after the command exited")
	}
	_ = cmd.Wait() // reaps it; the error is the signal that stopped it
}
