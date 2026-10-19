package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// A run with no ticket limit takes every ticket it has, however many DONE_SO_FAR counts: its START
// line says limit: none, its dispatch lines read [n], and it ends READY_EMPTY.
func TestRunWithNoLimitTakesEveryTicket(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Limit, h.cfg.DoneSoFar = NoLimit, 80
		for _, id := range []string{"A", "B", "C"} {
			h.beads.add(id, "ticket "+id, 1)
			h.worker(id, finishes(strings.ToLower(id)+".txt"))
		}
		o, code := h.run()

		if code != ExitOK || o.Final() != "READY_EMPTY after 83 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if !strings.Contains(h.sink.text(), "(done so far: 80, limit: none,") {
			t.Errorf("want the START line's limit: none\n%s", h.sink.text())
		}
		want := []string{"A [81] A dispatching: ticket A", "B [82] B dispatching: ticket B",
			"C [83] C dispatching: ticket C"}
		if got := h.sink.of(EvDispatch); !equal(got, want) {
			t.Errorf("dispatch events %q, want %q", got, want)
		}
	})
}

// A limit given stops the run once its dispatches, DONE_SO_FAR's included, reach it.
func TestLimitGivenStopsTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Limit, h.cfg.DoneSoFar = 3, 1
		for _, id := range []string{"A", "B", "C"} {
			h.beads.add(id, "ticket "+id, 1)
			h.worker(id, finishes(strings.ToLower(id)+".txt"))
		}
		o, code := h.run()

		if code != ExitOK || o.Final() != "LIMIT_REACHED at 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := []string{"A [2/3] A dispatching: ticket A", "B [3/3] B dispatching: ticket B"}
		if got := h.sink.of(EvDispatch); !equal(got, want) {
			t.Errorf("dispatch events %q, want %q", got, want)
		}
	})
}
