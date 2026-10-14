package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
)

// cancellingSink passes the events on to Sink, and Ctrl+C comes as one says on.
type cancellingSink struct {
	Sink
	on     string
	cancel context.CancelCauseFunc
}

func (s cancellingSink) Event(ev Event) {
	s.Sink.Event(ev)
	if strings.Contains(ev.Text, s.on) {
		s.cancel(InterruptedError("with Ctrl+C"))
	}
}

// orchestra-74b2.33: Ctrl+C as a run that has come to its end leaves its workers behind cuts short
// nothing it reports: it ends as it came to, READY_EMPTY, not INTERRUPTED.
func TestCtrlCAsTheRunLeavesItsWorkersBehindKeepsHowItEnded(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", asksAndStops(hooks.report))
		o := h.loop()
		o.ReportInterrupt = true
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		o.SetSink(cancellingSink{Sink: h.sink, on: "left for the next run: A", cancel: cancel})
		if code := o.Run(ctx); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q, want READY_EMPTY\n%s", code, o.Final(), h.sink.text())
		}
		if ctx.Err() == nil {
			t.Fatal("Ctrl+C never came")
		}
		if got := saved(t, h); len(got) != 1 || got[0].Ticket != "A" {
			t.Errorf("saved %+v, want A", got)
		}
	})
}
