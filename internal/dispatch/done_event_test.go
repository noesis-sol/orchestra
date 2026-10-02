package dispatch

import (
	"testing"
	"testing/synctest"
)

// The done event carries the tickets its text counts and the limit, for the terminal's closing line.
func TestDoneEventCarriesTheCountAndTheLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		limit, soFar int
		final        string
		n            int
	}{
		{10, 0, "READY_EMPTY after 1 tickets", 1},
		{3, 2, "LIMIT_REACHED at 3 tickets", 3},
	} {
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.cfg.Limit, h.cfg.DoneSoFar = tc.limit, tc.soFar
			h.beads.add("A", "first", 1)
			h.worker("A", finishes("a.txt"))
			o := h.loop()
			if code := o.Run(t.Context()); code != ExitOK || o.Final() != tc.final {
				t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			var done []Event
			for _, ev := range h.sink.events {
				if ev.Kind == EvDone {
					done = append(done, ev)
				}
			}
			if len(done) != 1 || done[0].N != tc.n || done[0].Limit != tc.limit {
				t.Errorf("done events %+v, want one with N %d and Limit %d", done, tc.n, tc.limit)
			}
		})
	}
}
