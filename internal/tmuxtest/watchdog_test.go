package tmuxtest

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// leftVar names, in the environment of the test binary started by TestAKilledTestLeavesNoServer,
// the file where TestKilledChild writes its socket's folder and the process IDs of its server and of
// what its pane runs.
const leftVar = "ORCHESTRA_TMUXTEST_LEFT"

// A test killed before its cleanups run, as one past its timeout is, leaves no tmux server behind,
// nor what the server's panes run, nor its socket's folder.
func TestAKilledTestLeavesNoServer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipped by -short: runs tmux")
	}
	Require(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	left := filepath.Join(t.TempDir(), "left")
	out, err := command.OutputWithEnv(context.Background(), command.ReadLimit, "", []string{leftVar + "=" + left},
		self, "-test.run=^TestKilledChild$")
	b, readErr := os.ReadFile(left)
	if readErr != nil {
		t.Fatalf("the test killed wrote nothing (%v): %v\n%s", readErr, err, out)
	}
	var sockets string
	var server, pane int
	if _, err := fmt.Sscan(string(b), &sockets, &server, &pane); err != nil {
		t.Fatalf("the test killed wrote %q: %v", b, err)
	}
	gone := func(pid int) bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		_, err := os.Stat(sockets)
		left := !errors.Is(err, os.ErrNotExist)
		if gone(server) && gone(pane) && !left {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("10s after its test was killed, its tmux server (process %d) runs: %v, its pane's process "+
				"%d runs: %v, its socket's folder (%s) is left: %v", server, !gone(server), pane, !gone(pane), sockets,
				left)
			// The processes are this test's own.
			_ = syscall.Kill(server, syscall.SIGKILL)
			_ = syscall.Kill(pane, syscall.SIGKILL)
			return
		}
	}
}

// TestKilledChild runs, as the test binary started by TestAKilledTestLeavesNoServer, a test with a
// tmux server whose pane runs sleep, which writes its socket's folder and the process IDs of the
// server and of the sleep, and is killed.
func TestKilledChild(t *testing.T) {
	left := os.Getenv(leftVar)
	if left == "" {
		t.Skip("run by TestAKilledTestLeavesNoServer")
	}
	socket := Server(t, "t", t.TempDir())
	pids, err := command.Output(context.Background(), command.ReadLimit, "", "tmux", "-L", socket, "new-window", "-d",
		"-P", "-F", "#{pid} #{pane_pid}", "sleep 600")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(left, []byte(os.Getenv("TMUX_TMPDIR")+" "+strings.TrimSpace(pids)), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = syscall.Kill(os.Getpid(), syscall.SIGKILL) // never returns
}
