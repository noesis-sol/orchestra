package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A worker blocked (or in a status Herdr can't tell) for a while, then idle at the end of its turn
// with its ticket in progress, is told to continue; the poll that tells it breaks the streak, so
// blocked again it gets the whole limit afresh, counted from the second block.
func TestNudgeBreaksTheBlockedStreak(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		st    AgentState
		limit time.Duration
		final string
	}{
		{StateBlocked, blockedLimit, "BLOCKED >4min: tab tab1 (A) needs attention"},
		{StateUnknown, unknownLimit,
			"UNKNOWN >5min: Herdr can't tell what the worker in tab tab1 (A) is doing; it needs attention"},
	} {
		t.Run(string(c.st), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				hooks := &hookReporter{}
				h.reporter = hooks
				h.beads.add("A", "first", 1)
				again := make(chan time.Time, 1)
				h.worker("A", func(w *fakeWorker) AgentState {
					hooks.report(w.wt, "PreToolUse")
					w.claim()
					w.shows(c.st)
					time.Sleep(3 * time.Minute) // well within either limit
					hooks.report(w.wt, "Stop")
					return "idle"
				}, func(w *fakeWorker) AgentState {
					again <- time.Now()
					return c.st
				})
				o, code := h.run()
				if code != ExitStuck || o.Final() != c.final {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if got := h.herdr.pastedTo(); strings.Join(got, ",") != "A" {
					t.Fatalf("messages pasted to %v, want one to A", got)
				}
				if took := time.Since(<-again); took <= c.limit || took > c.limit+2*statusPoll {
					t.Errorf("stopped %s after the second %s, want just past %s: the nudge breaks the streak",
						took, c.st, c.limit)
				}
			})
		})
	}
}
