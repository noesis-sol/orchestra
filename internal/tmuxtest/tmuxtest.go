// Package tmuxtest is for tests that run the real tmux: each gets a tmux server of its own, on a
// socket in a folder of its own, never the user's server, and the server is killed as the test ends.
//
// Without tmux installed the tests skip, unless ORCHESTRA_REQUIRE_TMUX=1, when they fail instead: CI
// sets it, so the tests can't be skipped there unseen.
package tmuxtest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/hookterm"
)

// RequireVar is the variable that, set to 1, has a test needing tmux fail where tmux isn't installed,
// rather than skip.
const RequireVar = "ORCHESTRA_REQUIRE_TMUX"

// Socket is the name of the socket of a test's tmux server (tmux -L), in a folder of the test's own.
const Socket = "orchestra-test"

// Server starts a tmux server of the test's own, with a detached session named session, its first
// window in dir, and returns the name of its socket (Socket). The test's tmux is kept from the
// user's: $TMUX and $TMUX_PANE are unset, and $TMUX_TMPDIR, where tmux keeps its sockets, is a
// folder of the test's own. The server's panes run /bin/sh, and get the test's environment as it is
// when Server is called. It skips the test where tmux isn't installed, or fails it with
// ORCHESTRA_REQUIRE_TMUX=1. The server is killed, and the test waits for it to be gone, as the test
// ends.
func Server(t *testing.T, session, dir string) string {
	t.Helper()
	Require(t)
	sockets, err := os.MkdirTemp("", "tmx") // short: a socket's path has a limit of about 100 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockets) }) // the server is gone by then (below); a leftover is no failure
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TMUX_TMPDIR", sockets)
	t.Setenv("SHELL", "/bin/sh") // the panes' shell, not the user's, with whatever their profile says
	path := filepath.Join(sockets, "tmux-"+strconv.Itoa(os.Getuid()), Socket)
	if _, err := command.Output(context.Background(), command.ReadLimit, "", "tmux", "-L", Socket, "-f", "/dev/null",
		"new-session", "-d", "-s", session, "-c", dir, "-x", "200", "-y", "50"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Killed even when the test failed with the server stuck: a test's server never outlives it.
		if _, err := command.Output(context.Background(), command.ReadLimit, "", "tmux", "-L", Socket,
			"kill-server"); err != nil && listening(path) {
			t.Errorf("the test's tmux server could not be killed: %v", err)
			return
		}
		for deadline := time.Now().Add(10 * time.Second); listening(path); time.Sleep(10 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Errorf("the test's tmux server (%s) is still running 10s after kill-server", path)
				return
			}
		}
	})
	return Socket
}

// Require skips the test where tmux isn't installed, or fails it there with ORCHESTRA_REQUIRE_TMUX=1.
func Require(t testing.TB) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		if os.Getenv(RequireVar) == "1" {
			t.Fatalf("tmux is not installed (%v), and %s=1 requires the tests that run it", err, RequireVar)
			return
		}
		t.Skipf("tmux is not installed (%v); %s=1 fails the test instead", err, RequireVar)
	}
}

// listening reports whether a tmux server still takes connections on the socket at path.
func listening(path string) bool { return hookterm.Probe(path) != hookterm.SocketGone }
