package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// A run killed (SIGKILL, a power cut) while it rebased a ticket to merge it leaves the rebase in
// progress in the ticket's worktree, on a detached HEAD, and saves nothing. The next run, carrying the
// ticket's worker over from what the run before saved, aborts the rebase, so the ticket merges again
// as one closed in its tab, rather than being set aside as off its branch.
func TestCarriedOverRebaseLeftInProgressIsAborted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Check = "true"
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
		if o, code := h.run(); code != ExitStuck || len(saved(t, h)) != 1 {
			t.Fatalf("run 1: exit %d, final %q, saved %+v\n%s", code, o.Final(), saved(t, h), h.sink.text())
		}

		// Its worker commits and closes A after the run; a run that started merging it was killed mid-rebase.
		wt := h.worktree("A")
		if err := h.mem.commitIn(wt, "A: add a.txt", "a.txt"); err != nil {
			t.Fatal(err)
		}
		h.beads.set("A", "closed")
		h.mem.mu.Lock()
		h.mem.rebasing[wt] = true
		h.mem.mu.Unlock()

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		want := "  A, carried over from the last run, had a rebase in progress in " + wt +
			", left by a run stopped while it merged A: aborted, so A merges again"
		if !strings.Contains(ev, want) {
			t.Errorf("run 2 events lack %q:\n%s", want, ev)
		}
		if strings.Contains(ev, "worktree not on wt/A") || strings.Contains(ev, "detached HEAD") {
			t.Errorf("A was set aside as off its branch:\n%s", ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v; want A", got)
		}
		if h.mem.inRebase(wt) {
			t.Errorf("%s is still mid-rebase", wt)
		}
	})
}
