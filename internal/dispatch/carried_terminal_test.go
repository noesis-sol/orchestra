package dispatch

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A run in tmux leaves A running; A's worker closes it after the run. A run in Herdr can't reach a
// worker in tmux: it says so in its START block, leaves A as it is (unmerged, its labels unchanged,
// B it blocks not started) and keeps it in the state file, where the next run in tmux finds it,
// merges it and starts B.
func TestWorkerLeftInTheOtherTerminalIsKeptForARunInIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Check = "true"
		h.cfg.Terminal, h.cfg.TerminalLabels = project.TerminalTmux, terminalLabels
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.link("B", "A", "blocks")
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
		h.worker("B", finishes("b.txt"))
		if o, code := h.run(); code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: A still in_progress") {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := []project.LeftWorker{{Ticket: "A", Agent: "A", Terminal: project.TerminalTmux, Tab: "tab1",
			Pane: "pane1", Worktree: h.worktree("A"), Left: "PAUSED"}}
		if got := saved(t, h); !reflect.DeepEqual(got, want) {
			t.Fatalf("saved %+v\nwant %+v", got, want)
		}
		if err := h.mem.commitIn(h.worktree("A"), "A: add a.txt", "a.txt"); err != nil {
			t.Fatal(err)
		}
		h.beads.set("A", "closed")
		a, _ := h.beads.Show(t.Context(), "A")
		labels := slices.Clone(a.Labels)

		h.cfg.Terminal = project.TerminalHerdr
		before := len(h.sink.text())
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()[before:]
		if !strings.Contains(ev, "  kept for a run in tmux, as this run in Herdr can't reach their workers: "+
			"A (left running: PAUSED)") {
			t.Errorf("run 2 doesn't say it keeps A for a run in tmux:\n%s", ev)
		}
		if strings.Contains(ev, "carried over from the last run") {
			t.Errorf("run 2 carried A over:\n%s", ev)
		}
		if got := closedIDs(h); len(got) != 0 {
			t.Errorf("run 2 merged %v; want nothing", got)
		}
		if n := dispatches(h, "B"); n != 0 {
			t.Errorf("run 2 started B, which waits on A, %d times", n)
		}
		if a, _ := h.beads.Show(t.Context(), "A"); !slices.Equal(a.Labels, labels) {
			t.Errorf("run 2 changed A's labels from %v to %v", labels, a.Labels)
		}
		if got := saved(t, h); !reflect.DeepEqual(got, want) {
			t.Fatalf("run 2 saved %+v\nwant %+v", got, want)
		}

		h.cfg.Terminal = project.TerminalTmux
		before = len(h.sink.text())
		if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 3: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if ev := h.sink.text()[before:]; !strings.Contains(ev, "  carried over from the last run: A (left running: PAUSED)") {
			t.Errorf("run 3 didn't carry A over:\n%s", ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A", "B"}) {
			t.Errorf("merged %v; want A, then B", got)
		}
	})
}

// Workers out of the maintainer's sight (tmux's detached session): the START block says how to see
// them, and so does the end of a run that leaves one in its tab.
func TestRunSaysHowToSeeWorkersOutOfSight(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Terminal, h.cfg.Workspace = project.TerminalTmux, "orchestra-app"
		h.cfg.TerminalLabels = terminalLabels
		h.cfg.Watch = "tmux attach -t orchestra-app"
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
		if o, code := h.run(); code != ExitStuck {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"terminal: tmux, session: orchestra-app,",
			"\n  to see the workers' tabs: tmux attach -t orchestra-app\n",
			"  left for the next run: A (left running: PAUSED)\n  to see their tabs: tmux attach -t orchestra-app\n",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
	})
}
