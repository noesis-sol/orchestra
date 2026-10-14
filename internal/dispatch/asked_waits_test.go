package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// B asks Q and is set aside until it is answered; back once it is, its worker finds B needs A's
// code, not merged yet, and stops with nothing done. B goes back in the queue, no longer set aside,
// and runs again once A merges.
func TestAnsweredTicketThatWaitsForMergeRunsAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "ticket A", 1)
		h.beads.add("B", "ticket B", 2)
		h.beads.add("C", "ticket C", 3)
		h.worker("A", slowly(10*idleGrace, "a.txt")) // still running when B's second worker stops
		h.worker("B", asksAndGoes, needs("A", StatusOpen), finishes("b.txt"))
		h.worker("C", answersThenFinishes("c.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 4 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if n := dispatches(h, "B"); n != 3 {
			t.Errorf("B dispatched %d times, want 3: asked, waits for A, then again once A merges\n%s",
				n, h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"C", "A", "B"}) {
			t.Errorf("merged %v, want C, A, then B\n%s", got, h.sink.text())
		}
		if got := h.sink.of(EvWaits); len(got) != 1 || !strings.HasPrefix(got[0], "B   WAITS_FOR_MERGE: B waits for A to merge") {
			t.Errorf("waits %q, want B waiting for A", got)
		}
		if aside := o.setAside(); len(aside) != 0 {
			t.Errorf("set aside: %v", aside)
		}
	})
}
