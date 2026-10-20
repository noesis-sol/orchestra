package cmux

import (
	"os"
	"testing"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// Without a socket named in the environment, cmux's CLI looks in ~/.local/state/cmux/cmux.sock and
// then for the sockets of cmux's other builds: no socket at the first doesn't say that no cmux runs.
// A call that fails is then an error, not NoServer, and a worker isn't read as gone.
func TestNoSocketInTheEnvironmentIsNoNoServer(t *testing.T) {
	f := newFakeCmux(t)
	t.Setenv("CMUX_SOCKET_PATH", "")
	home, err := os.MkdirTemp("", "cmx") // short: a socket's longer path would fail to dial as too long
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("HOME", home) // nothing at ~/.local/state/cmux/cmux.sock
	f.fail("tree", "Error: ERROR: request timed out")
	f.fail("ping", "Error: ERROR: request timed out")
	term := terminal(t)
	keep(t, term, book{Workspaces: map[string]workspaceEntry{workWS: {Label: "x-1"}},
		Surfaces: map[string]surfaceEntry{workSF: {Workspace: workWS, Agent: "x-1", Kind: "claude"}}})

	if err := term.Ping(bg); err == nil || NoServer(err) {
		t.Errorf("ping: %v, want an error that isn't NoServer", err)
	}
	if _, open, err := term.TabLabel(bg, workWS); err == nil || NoServer(err) {
		t.Errorf("tab label: open %v, %v, want an error that isn't NoServer", open, err)
	}
	if state, err := term.Status(bg, "x-1"); err == nil || state == agentio.StateGone {
		t.Errorf("status: %q %v, want an error and no state", state, err)
	}
}
