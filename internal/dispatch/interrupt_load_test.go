package dispatch

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
)

// interruptingTickets is the tracker, but Ctrl+C comes as Run asks bd show (show) or bd list for
// the closed tickets labelled UnmergedLabel (closed), and bd's command is cut short.
type interruptingTickets struct {
	Tickets
	interrupt    func()
	show, closed bool
}

func (i interruptingTickets) Show(ctx context.Context, id string) (Ticket, error) {
	if !i.show {
		return i.Tickets.Show(ctx, id)
	}
	i.interrupt()
	return Ticket{ID: id}, fmt.Errorf("bd show %s: %w", id, context.Cause(ctx))
}

func (i interruptingTickets) Closed(ctx context.Context, label string) ([]Ticket, error) {
	if !i.closed {
		return i.Tickets.Closed(ctx, label)
	}
	i.interrupt()
	return nil, fmt.Errorf("bd list --label %s: %w", label, context.Cause(ctx))
}

// runInterrupted runs once with the tracker cut short by Ctrl+C where show and closed say, and
// returns the loop and its exit code.
func runInterrupted(t *testing.T, h *harness, show, closed bool) (*Loop, int) {
	t.Helper()
	o := h.loop()
	o.ReportInterrupt = true
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	o.tickets = interruptingTickets{Tickets: o.tickets, show: show, closed: closed,
		interrupt: func() { cancel(InterruptedError("with Ctrl+C")) }}
	return o, o.Run(ctx)
}

// orchestra-xdlq: Ctrl+C as a run checks the workers the last one left behind drops none of them:
// the run ends INTERRUPTED with the state file as it was, and the next run carries them over.
func TestCtrlCWhileCarryingOverKeepsTheWorkers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", asksAndStops(hooks.report))
		if _, code := h.run(); code != ExitOK {
			t.Fatalf("run 1: exit %d\n%s", code, h.sink.text())
		}
		first := saved(t, h)
		if len(first) != 1 {
			t.Fatalf("run 1 left %+v; want A", first)
		}

		o, code := runInterrupted(t, h, true, false)
		if code != ExitInterrupted || !strings.HasPrefix(o.Final(), "INTERRUPTED: stopped with Ctrl+C") {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := saved(t, h); !reflect.DeepEqual(got, first) {
			t.Errorf("run 2 saved %+v\nwant it as run 1 left it: %+v", got, first)
		}
		if ev := h.sink.text(); strings.Contains(ev, "dropped") {
			t.Errorf("run 2 should drop nothing:\n%s", ev)
		}

		if _, code := h.run(); code != ExitOK {
			t.Fatalf("run 3: exit %d\n%s", code, h.sink.text())
		}
		if ev := h.sink.text(); !strings.Contains(ev, "  carried over from the last run: A (asked Q)") {
			t.Errorf("run 3 should carry A over:\n%s", ev)
		}
	})
}

// orchestra-xdlq: Ctrl+C as a run reads the tickets earlier runs left unmerged ends it
// INTERRUPTED, with its exit code, rather than READY_UNREADABLE.
func TestCtrlCWhileLoadingUnmergedIsInterrupted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		o, code := runInterrupted(t, h, false, true)
		if code != ExitInterrupted || !strings.HasPrefix(o.Final(), "INTERRUPTED: stopped with Ctrl+C") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if ev := h.sink.text(); strings.Contains(ev, string(stopReadyUnreadable)) {
			t.Errorf("events:\n%s", ev)
		}
		if n := dispatches(h, "A"); n != 0 {
			t.Errorf("A dispatched %d times; want none", n)
		}
	})
}

// The same in a scoped run, whose carried state the run would otherwise rewrite with only the
// workers outside its scope: Ctrl+C at either point leaves the file as it was.
func TestCtrlCWhileLoadingLeavesTheStateFile(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name         string
		show, closed bool
	}{
		{"loading unmerged", false, true},
		{"carrying over", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				hooks := &hookReporter{}
				h.reporter = hooks
				h.beads.add("A", "first", 1)
				h.beads.add("B", "second", 2)
				h.worker("A", asksAndStops(hooks.report))
				h.worker("B", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
				if _, code := h.run(); code != ExitStuck {
					t.Fatalf("run 1: exit %d\n%s", code, h.sink.text())
				}
				first := saved(t, h)
				if len(first) != 2 {
					t.Fatalf("run 1 left %+v; want A and B", first)
				}
				h.cfg.Ticket = "A"
				if _, code := runInterrupted(t, h, c.show, c.closed); code != ExitInterrupted {
					t.Fatalf("run 2: exit %d\n%s", code, h.sink.text())
				}
				if got := saved(t, h); !reflect.DeepEqual(got, first) {
					t.Errorf("run 2 saved %+v\nwant it as run 1 left it: %+v", got, first)
				}
			})
		})
	}
}

// A worker whose check Ctrl+C cut short is kept as it was, should anything save the workers left
// behind after loadCarried.
func TestCarriedCheckCutShortKeepsTheWorker(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", asksAndStops(hooks.report))
		if _, code := h.run(); code != ExitOK {
			t.Fatalf("run 1: exit %d\n%s", code, h.sink.text())
		}
		first := saved(t, h)
		o := h.loop()
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		o.tickets = interruptingTickets{Tickets: o.tickets, show: true,
			interrupt: func() { cancel(InterruptedError("with Ctrl+C")) }}
		o.loadCarried(ctx)
		o.saveCarried()
		if got := saved(t, h); !reflect.DeepEqual(got, first) {
			t.Errorf("saved %+v\nwant it as run 1 left it: %+v", got, first)
		}
	})
}
