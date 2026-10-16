package dispatch

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/project"
)

// orchestra-1bok: a run in tmux leaves A asked on Q; the maintainer answers Q, so bd ready lists A
// again. A run in Herdr can't reach A's worker, still in its tmux tab on A's worktree: it doesn't
// start a second worker there, nor B, which A blocks, and it saves A as the tmux run left it, once.
// The next run in tmux carries A over and resumes its worker.
func TestAnsweredTicketLeftInTheOtherTerminalGetsNoSecondWorker(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.Terminal, h.cfg.TerminalLabels = project.TerminalTmux, terminalLabels
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.link("B", "A", "blocks")
		h.worker("A", asksAndStops(hooks.report), finishes("a.txt"))
		h.worker("B", finishes("b.txt"))
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		first := saved(t, h)
		if len(first) != 1 || first[0].Ticket != "A" || first[0].Question != "Q" {
			t.Fatalf("run 1 left %+v; want A asked on Q", first)
		}
		h.beads.set("Q", "closed")

		h.cfg.Terminal = project.TerminalHerdr
		before := len(h.sink.text())
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()[before:]
		if !strings.Contains(ev, "  kept for a run in tmux, as this run in Herdr can't reach their workers: A (asked Q)") {
			t.Errorf("run 2 doesn't say it keeps A for a run in tmux:\n%s", ev)
		}
		if n := dispatches(h, "A"); n != 1 {
			t.Errorf("A dispatched %d times; want once, by run 1, and no second worker beside it", n)
		}
		if n := dispatches(h, "B"); n != 0 {
			t.Errorf("run 2 started B, which waits on A, %d times", n)
		}
		if got := closedIDs(h); len(got) != 0 {
			t.Errorf("run 2 merged %v; want nothing", got)
		}
		if got := saved(t, h); !reflect.DeepEqual(got, first) {
			t.Fatalf("run 2 saved %+v\nwant A once, as run 1 left it: %+v", got, first)
		}

		h.cfg.Terminal = project.TerminalTmux
		before = len(h.sink.text())
		if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("run 3: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if ev := h.sink.text()[before:]; !strings.Contains(ev, "  carried over from the last run: A (asked Q)") {
			t.Errorf("run 3 didn't carry A over:\n%s", ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A", "B"}) {
			t.Errorf("merged %v; want A, then B", got)
		}
	})
}

// orchestra-1bok: the nothing-to-run check agrees with the loop. A ready ticket whose worker the last
// run left in the other terminal is that worker's, and gives this run nothing to start.
func TestNothingToRunLeavesOutAReadyTicketKeptInTheOtherTerminal(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	b := newFakeBeads()
	b.add("A", "answered", 1)
	g := newFakeGit(c.Base)
	left := []project.LeftWorker{{Ticket: "A", Tab: "t1", Worktree: "/wt/A", Terminal: project.TerminalTmux,
		Question: "Q"}}
	if err := project.SaveState(c.Repo, project.RunState{Workers: left}); err != nil {
		t.Fatal(err)
	}
	if n, err := CheckNothingToRun(context.Background(), c, b, g, g); err != nil || n == nil {
		t.Errorf("A ready, its worker left in tmux: nothing to run %+v (%v); want nothing to run", n, err)
	}
}
