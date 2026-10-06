package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A worker gone from its tab, followed while the run takes no more tickets (its limit reached), is
// still resumed, or else dropped with a warning, rather than stopping the run with PAUSED: a run
// that stopped for it would save it for the next, which would stop for it again.

// orchestra-rrj4: the last run left A running (PAUSED), and its tab is closed by hand. The next run
// has its limit reached already (DONE_SO_FAR), so takes no more tickets; A, carried over, has its
// worker's session resumed all the same, and merges, while C waits.
func TestCarriedGoneWorkerIsResumedWhenTheRunTakesNoMore(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" }, finishes("a.txt"))
		if o, code := h.run(); code != ExitStuck {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if err := h.herdr.CloseTab(t.Context(), "tab1"); err != nil { // closed by hand
			t.Fatal(err)
		}

		h.beads.add("C", "third", 3)
		h.cfg.Limit, h.cfg.DoneSoFar = 1, 1
		o, code := h.run()
		if code != ExitOK || strings.Contains(o.Final(), "PAUSED") {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		starts := h.herdr.argsFor("A")
		if len(starts) != 2 || !resumedWith(starts[1], testSession) {
			t.Fatalf("starts %q; want A's worker resumed", starts)
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) || dispatches(h, "C") != 0 {
			t.Errorf("merged %v, C dispatched %d times; want A merged, C left", got, dispatches(h, "C"))
		}
		if notes := h.beads.notesOf("A"); strings.Count(notes, "is gone") != 1 {
			t.Errorf("notes on A: %q; want the one about its session resumed", notes)
		}
		if got := saved(t, h); len(got) != 0 {
			t.Errorf("saved for the next run: %+v", got)
		}
	})
}

// A, asked, has its worker take it up again in its tab once the question is answered, and then go,
// with no session recorded to resume. The run has started B, its limit, and takes no more tickets:
// A is warned about and dropped, rather than stopping the run with PAUSED, and isn't saved for the
// next run.
func TestAskedGoneWorkerIsDroppedWhenTheRunTakesNoMore(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.Limit = 2
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.add("C", "third", 3)
		answered := make(chan struct{})
		h.worker("A", asksThenCarriesOn(hooks, answered, func(w *fakeWorker) AgentState {
			time.Sleep(time.Minute)
			w.claim()
			return StateGone
		}))
		h.worker("B", answersAfter(time.Minute+7*time.Second, answered, time.Hour, "b.txt"))
		o, code := h.run()
		if code != ExitOK || strings.Contains(o.Final(), "PAUSED") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := "A   WORKER_GONE: A is in progress after its question Q, but its worker is gone from tab tab1 " +
			"(worktree " + h.worktree("A") + "), so it is no longer followed; " +
			"reopen it to run it again (bd update A --status open), or finish it by hand"
		if got := warnings(h, "A"); !equal(got, []string{want}) {
			t.Errorf("warnings:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
		}
		if got := closedIDs(h); !equal(got, []string{"B"}) {
			t.Errorf("merged %v; want B", got)
		}
		for _, w := range saved(t, h) {
			if w.Ticket == "A" {
				t.Errorf("A saved for the next run: %+v", w)
			}
		}
	})
}
