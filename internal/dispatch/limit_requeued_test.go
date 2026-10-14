package dispatch

import (
	"testing"
	"testing/synctest"
	"time"
)

// A ticket that started too early and went back in the queue to wait for its blocker counts toward
// Config.Limit once, as it starts again: with a limit of two, A and B both run to the end, B
// starting a second time once A merges, and only then does the run reach the limit, not with B left
// in the queue.
func TestRequeuedTicketCountsOnceTowardTheLimit(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency, h.cfg.Limit = 2, 2
		h.cfg.EnvHoldCount, h.cfg.EnvHoldWindow = 1, time.Hour
		h.beads.add("A", "ticket A", 1)
		h.beads.add("B", "ticket B", 2)
		h.worker("A", slowly(2*idleGrace, "a.txt"))
		h.worker("B", needs("A", StatusOpen), cutWith("A: add a.txt", "b.txt"))
		o, code := h.run()

		if code != ExitOK || o.Final() != "LIMIT_REACHED at 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"A", "B"}) {
			t.Errorf("merged %v, want A, then B\n%s", got, h.sink.text())
		}
		if got := h.dispatched(); !equal(got, []string{"A", "B", "B"}) {
			t.Errorf("dispatched %v, want A, B, then B again", got)
		}
		if got := h.sink.of(EvDispatch); len(got) != 3 || got[2] != "B [2/2] B dispatching: ticket B" {
			t.Errorf("dispatch events %q, want B's second as [2/2]", got)
		}
	})
}
