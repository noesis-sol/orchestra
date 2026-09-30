//go:build unix

package command

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// alive reports whether pid still runs, allowing a moment for a killed process to be reaped.
func alive(pid int) bool {
	for range 20 {
		if errors.Is(syscall.Kill(pid, syscall.Signal(0)), syscall.ESRCH) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
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
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	// The child ignores SIGTERM and keeps the output pipe open, so only the group's SIGKILL ends it.
	_, err := GroupOutput(ctx, time.Second, dir, "sh", "-c", `trap "" TERM; sleep 30 & echo $! > pid; wait`)
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("returned after %s, want within the timeout plus grace", took)
	}
	if err == nil {
		t.Error("a timed-out command should fail")
	}
	if pid := childPID(t, filepath.Join(dir, "pid")); alive(pid) {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Errorf("child %d still runs", pid)
	}
}

func TestGroupOutputPassesAndCleansUpAfterABackgroundChild(t *testing.T) {
	dir := t.TempDir()
	out, err := GroupOutput(context.Background(), 200*time.Millisecond, dir, "sh", "-c", `sleep 30 > /dev/null & echo $! > pid; echo ok`)
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Errorf("got %q, %v", out, err)
	}
	if pid := childPID(t, filepath.Join(dir, "pid")); alive(pid) {
		syscall.Kill(pid, syscall.SIGKILL)
		t.Errorf("child %d still runs", pid)
	}
}

func TestGroupOutputReportsFailure(t *testing.T) {
	if _, err := GroupOutput(context.Background(), time.Second, t.TempDir(), "sh", "-c", "exit 3"); err == nil {
		t.Error("a failing command should fail")
	}
}
