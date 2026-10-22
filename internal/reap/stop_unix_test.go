//go:build unix

package reap

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

// agent starts a stand-in for a worker's Claude Code: a shell that starts script in a process group
// of its own, as Claude Code starts its Bash commands, and waits. It returns the shell and the
// process ID script wrote to the file named by its $1. Both are killed when the test ends.
func agent(t *testing.T, script string) (*exec.Cmd, int) {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "pid")
	cmd := exec.Command("sh", "-c", "set -m; sh -c '"+script+"' _ \"$1\" & wait", "_", pidFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait() // reaped, however it ends; the test reads how by the process listing
		close(done)
	}()
	t.Cleanup(func() {
		_ = cmd.Process.Kill() // gone already, as a rule
		<-done
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, err := os.ReadFile(pidFile)
		if pid, aerr := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && aerr == nil && pid > 0 {
			t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) }) // gone already, if the test passed
			return cmd, pid
		}
		if time.Now().After(deadline) {
			t.Fatal("the agent's command never wrote its process ID")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// running reports whether pid runs still, by the process listing: a zombie has ended.
func running(t *testing.T, pid int) bool {
	t.Helper()
	l, err := list(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	p, ok := l[pid]
	return ok && !p.zombie
}

// gone waits a little for pid to end, and reports whether it did.
func gone(t *testing.T, pid int) bool {
	t.Helper()
	for range 50 {
		if !running(t, pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// tab is a fake terminal's tab running a: closing it kills a, as an agent whose shutdown doesn't
// finish, or fails with closeErr.
type tab struct {
	a        *exec.Cmd
	closeErr error
	closed   bool
}

func (f *tab) TabProcesses(context.Context, string) ([]int, error) {
	if f.closed {
		return nil, errors.New("asked after the tab was closed")
	}
	return []int{f.a.Process.Pid}, nil
}

func (f *tab) CloseTab(context.Context, string) error {
	if f.closeErr != nil {
		return f.closeErr
	}
	f.closed = true
	return f.a.Process.Signal(syscall.SIGKILL)
}

func TestCloseStopsADetachedCommand(t *testing.T) {
	a, cmd := agent(t, `sleep 300 & echo $! > "$1"; wait`)
	if err := Close(t.Context(), &tab{a: a}, "@1"); err != nil {
		t.Fatal(err)
	}
	if !gone(t, cmd) {
		t.Error("the command the agent left running in a group of its own runs still once its tab is closed")
	}
}

func TestCloseLeavesATabItCouldNotClose(t *testing.T) {
	a, cmd := agent(t, `sleep 300 & echo $! > "$1"; wait`)
	refused := errors.New("refused")
	if err := Close(t.Context(), &tab{a: a, closeErr: refused}, "@1"); !errors.Is(err, refused) {
		t.Fatalf("Close = %v, want the terminal's error", err)
	}
	if !running(t, cmd) {
		t.Error("the command was stopped, though its tab was left open")
	}
}

func TestStopKillsWhatOutlivesSIGTERM(t *testing.T) {
	a, cmd := agent(t, `trap "" TERM; echo $$ > "$1"; while :; do sleep 0.05; done`)
	tree, err := Collect(t.Context(), a.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	tree.term = 200 * time.Millisecond
	if err := a.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	tree.Stop(t.Context())
	if !gone(t, cmd) {
		t.Error("the command that ignores SIGTERM runs still")
	}
}

func TestStopGivesTheAgentsShutdownTimeFirst(t *testing.T) {
	a, cmd := agent(t, `sleep 300 & echo $! > "$1"; wait`)
	tree, err := Collect(t.Context(), a.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	tree.shutdown = 300 * time.Millisecond
	start := time.Now()
	tree.Stop(t.Context()) // the agent runs on, so Stop waits out its shutdown's time
	if d := time.Since(start); d < tree.shutdown {
		t.Errorf("Stop returned after %v, before the agent's shutdown had %v", d, tree.shutdown)
	}
	if !gone(t, cmd) {
		t.Error("the command runs still once the agent's shutdown had its time")
	}
}
