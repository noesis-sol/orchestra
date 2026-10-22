package dispatch

import (
	"path/filepath"
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

// Every requeue says so with an EvRequeued, whatever put the ticket back in the queue, so that the
// dashboard shows it waiting rather than working (orchestra-4n6j.6): a ticket whose setup failed had
// no record of it but a warning the dashboard doesn't show, and stayed "working" to the run's end.

// requeuedOf is what the run's EvRequeued records said: each one's ticket and why.
func requeuedOf(h *harness) [][2]string {
	var l [][2]string
	for _, ev := range eventsOf(h, EvRequeued) {
		l = append(l, [2]string{ev.Ticket, ev.Detail})
	}
	return l
}

// checkRequeuedBefore checks that each of the run's events of kind k comes just after an EvRequeued of
// its ticket, and that EvRequeued has no line in the log.
func checkRequeuedBefore(t *testing.T, h *harness, k Kind) {
	t.Helper()
	h.sink.mu.Lock()
	defer h.sink.mu.Unlock()
	for i, ev := range h.sink.events {
		if ev.Kind == k && (i == 0 || h.sink.events[i-1].Kind != EvRequeued || h.sink.events[i-1].Ticket != ev.Ticket) {
			t.Errorf("%s of %s doesn't follow its EvRequeued", k, ev.Ticket)
		}
		if ev.Kind == EvRequeued && ev.Text != "" {
			t.Errorf("EvRequeued of %s has a line: %q", ev.Ticket, ev.Text)
		}
	}
}

// A ticket whose worktree's setup failed is back in the queue: EvRequeued, its setup failed.
func TestAFailedSetupIsRequeued(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		cmd, _ := recordsSetups(t, failsIn("A", filepath.Join(t.TempDir(), "never")))
		h.cfg.Setup = cmd
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := requeuedOf(h); !slices.Equal(got, [][2]string{{"A", "its setup failed"}}) {
			t.Errorf("requeued %q, want A once, its setup failed\n%s", got, h.sink.text())
		}
		checkRequeuedBefore(t, h, EvWarn)
	})
}

// A ticket whose worker stopped with nothing done, waiting for a ticket to merge, is requeued just
// before its EvWaits.
func TestATicketThatWaitsForAMergeIsRequeued(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "ticket A", 1)
		h.beads.add("B", "ticket B", 2)
		h.worker("A", slowly(10*idleGrace, "a.txt"))
		h.worker("B", needs("A", StatusOpen), finishes("b.txt"))
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := requeuedOf(h); !slices.Equal(got, [][2]string{{"B", "it waits for A to merge"}}) {
			t.Errorf("requeued %q, want B waiting for A\n%s", got, h.sink.text())
		}
		checkRequeuedBefore(t, h, EvWaits)
	})
}

// Tickets a hold for the environment reopens are requeued just before their EvReopened.
func TestTicketsReopenedByAnEnvironmentHoldAreRequeued(t *testing.T) {
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
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.logged())
		}
		held := "the run held for the environment"
		if got := requeuedOf(h); !slices.Equal(got, [][2]string{{"A", held}, {"B", held}}) {
			t.Errorf("requeued %q, want A and B, as the run held for the environment", got)
		}
		checkRequeuedBefore(t, h, EvReopened)
	})
}
