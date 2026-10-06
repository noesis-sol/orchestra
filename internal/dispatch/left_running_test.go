package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/project"
)

// leftRunning is the line saying ticket id, left running in tab when the run ended, is labelled.
func leftRunning(id, tab string) string {
	return id + " is left running in tab " + tab + " and labelled 'unmerged': " +
		"tickets it blocks wait until wt/" + id + " is merged into main, in later runs too"
}

// before reports whether a comes before b in s, both in it.
func before(s, a, b string) bool {
	i, j := strings.Index(s, a), strings.Index(s, b)
	return i >= 0 && j >= 0 && i < j
}

// A worker left running when the run stops may close its ticket once orchestra has gone. The next
// run merges it, from the state file (see carry_test.go); with that file lost, nothing does. Its
// ticket is labelled unmerged, so the tickets it blocks wait for it in later runs all the same,
// until it is merged by hand.
func TestTicketLeftRunningHoldsItsDependentsInLaterRuns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.link("B", "A", "blocks")
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
		h.worker("B", finishes("b.txt"))
		o, code := h.run()
		if code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: A still in_progress") {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if a, _ := h.beads.Show(t.Context(), "A"); !HasLabel(a, UnmergedLabel) {
			t.Fatalf("A should be labelled %q: %v", UnmergedLabel, a.Labels)
		}
		if ev := h.sink.text(); !before(ev, leftRunning("A", "tab1"), o.Final()) {
			t.Errorf("A's label should be said before the final line; events:\n%s", ev)
		}

		// Answered in its tab, the worker commits and closes A after the run.
		if err := h.mem.commitIn(h.worktree("A"), "A: add a.txt", "a.txt"); err != nil {
			t.Fatal(err)
		}
		h.beads.set("A", "closed")
		if err := os.Remove(filepath.Join(h.repo, project.RunPath(project.StateName))); err != nil {
			t.Fatal(err) // the state file lost
		}

		// Run 2: bd ready lists B, since A is closed, but A's code is not on main.
		if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if ev := h.sink.text(); !strings.Contains(ev, "B waits: A closed but not merged (left unmerged by an earlier run)") {
			t.Errorf("events:\n%s", ev)
		}

		// The maintainer merges A by hand; run 3 finds it on main, removes its label and starts B.
		if _, err := h.mem.FastForward(t.Context(), h.repo, "wt/A"); err != nil {
			t.Fatal(err)
		}
		o, code = h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 3: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if a, _ := h.beads.Show(t.Context(), "A"); HasLabel(a, UnmergedLabel) {
			t.Errorf("A is on main, so its label should be removed: %v", a.Labels)
		}
		if log := h.mainLog(); !strings.Contains(log, "B: add b.txt") {
			t.Errorf("main:\n%s", log)
		}
	})
}

// A ticket left running and still open later is dispatched again as usual, and loses its label
// when it merges.
func TestTicketLeftRunningLosesItsLabelWhenItMergesLater(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.link("B", "A", "blocks")
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" }, finishes("a.txt"))
		h.worker("B", finishes("b.txt"))
		if o, code := h.run(); code != ExitStuck {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		h.beads.set("A", "open") // the maintainer reopens it
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if a, _ := h.beads.Show(t.Context(), "A"); HasLabel(a, UnmergedLabel) {
			t.Errorf("A merged, so its label should be removed: %v", a.Labels)
		}
		if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || !strings.Contains(log, "B: add b.txt") {
			t.Errorf("main:\n%s", log)
		}
	})
}

// Ctrl+C leaves the running worker, which may close its ticket after the run: it is labelled too,
// before the INTERRUPTED line.
func TestInterruptLabelsTheTicketLeftRunning(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		started, release := make(chan struct{}), make(chan struct{})
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); close(started); <-release; return "idle" })
		defer close(release)
		o := h.loop()
		o.ReportInterrupt = true
		ctx, cancel := context.WithCancelCause(t.Context())
		codes := make(chan int, 1)
		go func() { codes <- o.Run(ctx) }()
		<-started
		cancel(InterruptedError("with Ctrl+C"))
		if code := <-codes; code != ExitInterrupted {
			t.Fatalf("exit %d, final %q", code, o.Final())
		}
		if a, _ := h.beads.Show(t.Context(), "A"); !HasLabel(a, UnmergedLabel) {
			t.Errorf("A should be labelled %q: %v", UnmergedLabel, a.Labels)
		}
		if ev := h.sink.text(); !before(ev, leftRunning("A", "tab1"), "INTERRUPTED: ") {
			t.Errorf("A's label should be said before the INTERRUPTED line; events:\n%s", ev)
		}
	})
}

// A ticket left running that bd can't label is warned about, with the command to label it by hand;
// one labelled already isn't labelled again.
func TestTicketLeftRunningLabelFailureIsWarned(t *testing.T) {
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordSink{}
	o := New(Config{Repo: "repo", Base: "main"}, log, "", Deps{Notes: brokenBd(nil), Merger: upToDate{}})
	o.SetSink(sink)
	o.setActive(Status{Ticket: "A", Tab: "tab1"})
	o.setActive(Status{Ticket: "B", Tab: "tab2"})
	o.setLabelled("B", true) // left unmerged in this run, say, before something stopped it
	o.leaveRunning(context.Background())
	want := "  LABEL_FAILED: bd could not label A 'unmerged': bd defer A: exit status 1: Error: database is locked " +
		"(another bd holds it); later runs may start the tickets it blocks before it merges (bd label add A unmerged)"
	if got := sink.text(); got != want+"\n" {
		t.Errorf("events:\n%s\nwant:\n%s", got, want)
	}
}
