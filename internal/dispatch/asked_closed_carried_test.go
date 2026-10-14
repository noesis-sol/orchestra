package dispatch

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// orchestra-74b2.20: an asked ticket its worker closes while the run holds for a failure isn't
// merged by that run (ASKED_UNMERGED). It is left for the next run, as the line says, rather than
// for review: the run saves it, and the next one merges it and removes its unmerged label.
func TestAskedTicketClosedAsTheRunEndsIsMergedByTheNext(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.Concurrency = 3
		h.cfg.Check = "true"
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.add("C", "third", 3)
		h.worker("A", asksThenCarriesOn(hooks, h.sink.held, func(w *fakeWorker) AgentState {
			w.commit("a.txt")
			w.close()
			return "idle"
		}))
		h.worker("B", func(w *fakeWorker) AgentState { w.claim(); return "idle" }) // pauses the run
		h.worker("C", busyFor(time.Hour, "c.txt"))
		if o, code := h.run(); code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: B still in_progress") {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := "  ASKED_UNMERGED: A was closed in tab " + tabOf(h, "A") + " after its question Q, and the run ends " +
			"before merging it; worktree " + h.worktree("A") + " left for the next run to merge, labelled 'unmerged'"
		if !slices.ContainsFunc(h.sink.events, func(ev Event) bool {
			return ev.Kind == EvWarn && ev.Ticket == "A" && strings.HasPrefix(ev.Text, want)
		}) {
			t.Errorf("run 1 events lack %q:\n%s", want, h.sink.text())
		}
		if got := ticketsOf(saved(t, h)); !slices.Contains(got, "A") {
			t.Fatalf("run 1 saved %v; want A among them, for the next run to merge", got)
		}
		if ev := h.sink.text(); !strings.Contains(ev, "  left for the next run: A (asked Q)") {
			t.Errorf("run 1 should say it leaves A for the next:\n%s", ev)
		}

		// Between runs, B's worker finishes it in its tab.
		h.beads.set("B", "closed")
		if err := h.mem.commitIn(h.worktree("B"), "B: add b.txt", "b.txt"); err != nil {
			t.Fatal(err)
		}

		if o, code := h.run(); code != ExitOK {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !slices.Contains(got, "A") {
			t.Errorf("merged %v; want A among them\n%s", got, h.sink.text())
		}
		if a, _ := h.beads.Show(t.Context(), "A"); HasLabel(a, UnmergedLabel) {
			t.Errorf("A, merged, should no longer be labelled %q: %v", UnmergedLabel, a.Labels)
		}
		if got := ticketsOf(saved(t, h)); slices.Contains(got, "A") {
			t.Errorf("run 2 saved %v; A is merged", got)
		}
	})
}
