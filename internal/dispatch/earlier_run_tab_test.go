package dispatch

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

// A worker an earlier run left idle in its tab, as it set its ticket aside, is no record of this
// run's: when the ticket is dispatched again, the terminal finds its tab under the ticket's name, and
// the tab is closed rather than the worker renamed, as one this run left is.

// A's worker commits and defers A, and the run leaves it idle in its tab; A is undeferred by hand.
// The next run, dispatching A again, closes that tab, naming the earlier session's transcript, and
// A's new worker has the only tab labelled A.
func TestEarlierRunsIdleWorkerHasItsTabClosed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "a", 1)
		tabs := -1
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.commit("a.txt")
			w.deferIt()
			return "idle"
		}, func(w *fakeWorker) AgentState {
			tabs = openTabsOf(h, "A")
			return finishes("b.txt")(w)
		})
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		earlier := tabOf(h, "A")
		h.beads.set("A", "open") // bd undefer A

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if !slices.Contains(h.herdr.tabsClosed(), earlier) {
			t.Errorf("A's earlier tab %s was not closed; tabs closed %v\n%s", earlier, h.herdr.tabsClosed(), h.logged())
		}
		if ev := h.sink.text(); strings.Contains(ev, "renamed to") {
			t.Errorf("A's earlier worker was renamed, not closed:\n%s", ev)
		}
		want := "  A's earlier worker was idle in tab " + earlier + "; its tab is closed, as the new worker takes up " +
			"its worktree; its session's transcript is /home/u/.claude/projects/x/" + testSession + ".jsonl"
		if !strings.Contains(h.logged(), want) {
			t.Errorf("the log lacks %q:\n%s", want, h.logged())
		}
		if tabs != 1 {
			t.Errorf("A's new worker ran beside %d tabs labelled A, want its own alone", tabs)
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v, want A", got)
		}
	})
}

// An earlier worker idle under A's name in a tab labelled otherwise now, as one relabelled by hand,
// may not be A's to close: it is renamed, and its tab left open.
func TestEarlierWorkerInATabLabelledOtherwiseIsRenamed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "a", 1)
		h.worker("A", finishes("a.txt"))
		h.herdr.panes["earlier"] = fakePane{tab: "kept"}
		h.herdr.labels["kept"] = "notes"
		h.herdr.agents = append(h.herdr.agents, &fakeAgent{name: "A", kind: "claude", pane: "earlier", status: "idle"})

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if slices.Contains(h.herdr.tabsClosed(), "kept") {
			t.Errorf("tab kept, labelled notes, was closed\n%s", h.logged())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"  A's earlier worker is idle in tab kept; tab kept left open: the terminal has it labelled 'notes' now",
			"  earlier worker for A renamed to A-1; its tab is left open",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
	})
}
