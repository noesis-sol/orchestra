package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A and B fail at once and are set aside; the hold for the environment reopens them, each with a
// reopened event, and they are no longer set aside. After the probe the ticket limit lets only A
// run again: it merges, and B, open and in the queue, isn't counted set aside when the run ends.
func TestTicketsReopenedByAnEnvironmentHoldAreNotSetAside(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		h.cfg.EnvProbe = time.Minute
		h.cfg.Limit = 3
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.worker("A", givesUp, finishes("a.txt"))
		h.worker("B", givesUp)
		h.worker(probeID, runsProbe)

		o, code := h.run()
		if code != ExitOK || o.Final() != "LIMIT_REACHED at 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.logged())
		}
		if got := h.sink.of(EvReopened); len(got) != 2 || !strings.HasPrefix(got[0], "A   A reopened") ||
			!strings.HasPrefix(got[1], "B   B reopened") {
			t.Errorf("reopened %q, want A and B", got)
		}
		var done *Event
		for i, ev := range h.sink.events {
			if ev.Kind == EvDone {
				done = &h.sink.events[i]
			}
		}
		if done == nil || done.Closed != 1 || done.SetAside != 0 {
			t.Fatalf("done %+v, want 1 closed and none set aside", done)
		}
		if got := h.alerts.list(); len(got) == 0 || got[len(got)-1] != "Finished the run · 1 ticket closed" {
			t.Errorf("notified %q, want the run finished with nothing set aside", got)
		}
		if st := h.statusOf("B"); st != "open" {
			t.Errorf("B is %s, want open", st)
		}
	})
}
