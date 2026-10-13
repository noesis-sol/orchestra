package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/project"
)

// Under cmux each worker has a workspace of its own: the START block says so rather than naming one.
func TestStartSaysCmuxWorkersHaveWorkspacesOfTheirOwn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Terminal, h.cfg.Workspace = project.TerminalCmux, ""
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); w.close(); return "idle" })
		h.run()
		if ev := h.sink.text(); !strings.Contains(ev, "terminal: cmux, workspaces: one per ticket,") {
			t.Errorf("the START block doesn't say where the workers are:\n%s", ev)
		}
	})
}
