package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A resumed worker that doesn't take the message it was launched with gets it pasted, though its
// ticket is in progress from the worker before it, and carries on; its ticket merges.
func TestResumedWorkerThatDropsItsMessageGetsItPasted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.herdr.resumeDrops["A"] = true
		h.beads.add("A", "first", 1)
		h.worker("A", vanishes, finishes("a.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v; want A", got)
		}
		texts := h.herdr.texts["A"]
		if len(texts) != 1 || !strings.Contains(texts[0], "Carry on with A where you left off") {
			t.Errorf("pasted to A's worker %q; want its message to carry on, once", texts)
		}
		if !strings.Contains(h.logged(), "A did not start on the prompt given at launch; pasting it") {
			t.Errorf("the log should say the message was pasted:\n%s", h.logged())
		}
	})
}

// A resumed worker that takes its message neither at launch nor pasted stops the run (PAUSED) as one
// that never took up carrying on, at once rather than after the idle grace.
func TestResumedWorkerThatNeverTakesItsMessageIsReported(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.herdr.resumeDrops["A"] = true
		h.herdr.promptFails["A"] = true
		h.beads.add("A", "first", 1)
		h.worker("A", vanishes)
		begun := time.Now()
		o, code := h.run()
		want := "PAUSED: A's worker, resumed in tab tab2, never took up carrying on (worktree " + h.worktree("A") +
			"); stopping so it can be looked at"
		if code != ExitStuck || o.Final() != want {
			t.Fatalf("exit %d, final %q\nwant %q\n%s", code, o.Final(), want, h.sink.text())
		}
		if took := time.Since(begun); took >= 5*time.Minute {
			t.Errorf("the run took %v to stop; want it soon after the worker was gone, not after the idle grace", took)
		}
		if notes := h.beads.notesOf("A"); !strings.Contains(notes, "the worker resumed in Herdr tab tab2 never took up carrying on") {
			t.Errorf("notes on A: %q", notes)
		}
	})
}
