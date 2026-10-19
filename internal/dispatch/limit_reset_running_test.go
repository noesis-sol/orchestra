package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// An organ refused for the usage limit, with its reset, holds the run while a worker runs on, never
// limited: at the reset the hold lifts, and the free slot takes the ticket that waited, long before
// that worker finishes. The dashboard's usage-limit note goes with the lifting (EvLimitLift).
func TestAnOrganHoldLiftsAtTheResetWhileWorkersRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.Concurrency = 2
		h.cfg.UsageLimitWait = 6 * time.Hour
		start := time.Now()
		reset := start.Add(time.Hour).Truncate(time.Minute)
		said := "You've hit your session limit · resets " + reset.Local().Format("3:04pm")
		var o *Loop
		h.beads.add("A", "runs for hours", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			o.organLimited("triage of Q", said)
			readyOnceHeld(h, "B", "ready while the run holds", 2)
			return carriesOn(hooks, "a.txt", 3*time.Hour)(w)
		})
		h.worker("B", finishes("b.txt"))
		o = h.loop()
		if code := o.Run(context.Background()); code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		for _, f := range []string{"A: add a.txt", "B: add b.txt"} {
			if !strings.Contains(h.mainLog(), f) {
				t.Errorf("main lacks %q:\n%s", f, h.mainLog())
			}
		}
		held, lifted := h.sink.eventsOf(EvLimited), h.sink.eventsOf(EvLimitLift)
		if len(held) != 1 || !held[0].Reset.Equal(reset) {
			t.Fatalf("usage limit events: %+v\n%s", held, h.sink.text())
		}
		if len(lifted) != 1 || lifted[0].Time.Before(reset.Add(resetGrace)) ||
			lifted[0].Time.After(reset.Add(resetGrace+time.Second)) {
			t.Fatalf("lifted events: %+v, want one at %s\n%s", lifted, reset.Add(resetGrace), h.sink.text())
		}
		if want := "USAGE_LIMIT lifted: the limit reset at " + ResetLabel(reset, start) + "; taking tickets again"; lifted[0].Text != want {
			t.Errorf("lifted: %q, want %q", lifted[0].Text, want)
		}
		if b := h.sink.dispatchedAt("B"); b.Before(reset.Add(resetGrace)) || b.After(start.Add(2*time.Hour)) {
			t.Errorf("B started at %s, want at the reset and its minute (%s), before A finishes (%s)",
				b, reset.Add(resetGrace), start.Add(3*time.Hour))
		}
		if strings.Contains(h.logged(), "told to carry on") {
			t.Errorf("a worker was told to carry on:\n%s", h.logged())
		}
	})
}
