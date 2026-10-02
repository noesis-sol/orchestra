package dispatch

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

// A worker the last run left behind is merged with the tab ID that run recorded. Herdr numbers its
// tabs afresh when it starts without restoring its last session, so by then the ID may name another
// tab, the user's own included: the tab is closed only while Herdr still has it labelled with the
// ticket's ID, and the line saying the ticket merged says what became of it.
func TestCarriedOverWorkersTabIsClosedOnlyWhileItIsTheWorkers(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		herdr  func(t *testing.T, h *fakeHerdr) // between the runs
		closed bool                             // tab1 is closed as A merges
		says   string                           // after "merged into main, "
	}{
		{"kept", func(*testing.T, *fakeHerdr) {}, true, "worktree, branch and tab removed"},
		{"another tab under its ID", func(t *testing.T, h *fakeHerdr) {
			h.restart()
			if tab, _, err := h.CreateTab(t.Context(), "w1", "/home/me", "notes"); err != nil || tab != "tab1" {
				t.Fatalf("the user's tab is %q (%v); want tab1, the ID A's worker had", tab, err)
			}
		}, false, "worktree and branch removed; " +
			"tab tab1 left open: Herdr has it labelled 'notes' now, not A, so it may not be its worker's"},
		{"no tab under its ID", func(_ *testing.T, h *fakeHerdr) { h.restart() }, false,
			"worktree and branch removed; its tab tab1 was closed already"},
		{"Herdr can't say", func(_ *testing.T, h *fakeHerdr) { h.labelFails = true }, false,
			"worktree and branch removed; tab tab1 left open, as Herdr can't say whose it is"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.beads.add("A", "first", 1)
				h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
				if o, code := h.run(); code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: A still in_progress") {
					t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				// Answered in its tab, the worker commits and closes A after the run.
				if err := h.mem.commitIn(h.worktree("A"), "A: add a.txt", "a.txt"); err != nil {
					t.Fatal(err)
				}
				h.beads.set("A", "closed")
				c.herdr(t, h.herdr)

				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
					t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				merged := h.sink.of(EvClosed)
				if len(merged) != 1 || !strings.HasPrefix(merged[0], "A   A closed (") ||
					!strings.HasSuffix(merged[0], "; merged into main, "+c.says) {
					t.Errorf("merged:\n%s\nwant A's line to end in %q", strings.Join(merged, "\n"), c.says)
				}
				if got := slices.Contains(h.herdr.tabsClosed(), "tab1"); got != c.closed {
					t.Errorf("tab1 closed: %v, want %v", got, c.closed)
				}
				if h.herdr.labelFails && !strings.Contains(h.logged(), "cannot tell whether tab tab1 is still A's") {
					t.Errorf("the log should say why tab1 was left open:\n%s", h.logged())
				}
			})
		})
	}
}
