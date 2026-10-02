package dispatch

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// asksAndStops is a worker turn that asks the maintainer and ends, at its Stop hook, without
// reopening its ticket: the ticket stays in progress, waiting on the question.
func asksAndStops(report func(wt, event string)) behaviour {
	return func(w *fakeWorker) AgentState {
		report(w.wt, "PreToolUse")
		w.claim()
		w.ask("Q", "which way?")
		report(w.wt, "Stop")
		return "idle"
	}
}

// A worker that reports through hooks and ends its turn after asking, its ticket left in progress,
// settles at that Stop: the run says it asked, reopens the ticket and starts the next one at once,
// rather than after the idle grace.
func TestAskingWorkerSettlesAtItsStop(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", asksAndStops(hooks.report))
		h.worker("B", finishes("b.txt"))
		start := time.Now()
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if took := time.Since(start); took >= time.Minute {
			t.Errorf("the run took %s: A's worker should settle at its Stop, and B start at once", took)
		}
		if got := h.sink.of(EvAsked); len(got) != 1 || !strings.HasPrefix(got[0], "A   ASKED: A waits on your answer to Q (which way?)") {
			t.Errorf("asked:\n%s", strings.Join(got, "\n"))
		}
		if a, _ := h.beads.Show(context.Background(), "A"); a.Status != "open" {
			t.Errorf("A is %s, want it reopened, to come back once Q is answered", a.Status)
		}
		if log := h.mainLog(); !strings.Contains(log, "B: add b.txt") {
			t.Errorf("main:\n%s", log)
		}
		if got := h.herdr.pastedTo(); len(got) != 0 {
			t.Errorf("messages pasted to %v", got)
		}
		if logged := h.logged(); !strings.Contains(logged, "A settled: Stop hook at ") || !strings.Contains(logged, ", waiting on Q") {
			t.Errorf("the log should say A settled at its Stop hook, waiting on Q:\n%s", logged)
		}
	})
}

// A worker told to continue as many times as it may be, which then asks in its next turn, still
// settles at that turn's Stop.
func TestWorkerAskingOnceToldToContinueSettlesAtItsStop(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", stopsMidTicket(hooks.report), stopsMidTicket(hooks.report), asksAndStops(hooks.report))
		start := time.Now()
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if took := time.Since(start); took >= time.Minute {
			t.Errorf("the run took %s: A's worker should settle at the Stop of the turn it asked in", took)
		}
		if got := h.herdr.pastedTo(); strings.Join(got, ",") != "A,A" {
			t.Errorf("messages pasted to %v, want two to A", got)
		}
		if got := h.sink.of(EvAsked); len(got) != 1 {
			t.Errorf("asked: %q", got)
		}
	})
}

// A worker without hooks can't tell the end of its turn from a wait on its own background command:
// one that asks and leaves its ticket in progress still settles only after the idle grace.
func TestAskingWorkerWithoutHooksSettlesAfterTheIdleGrace(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); w.ask("Q", "which way?"); return "idle" })
		start := time.Now()
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if took := time.Since(start); took < idleGrace {
			t.Errorf("the run took %s, less than the idle grace", took)
		}
		if got := h.sink.of(EvAsked); len(got) != 1 {
			t.Errorf("asked: %q", got)
		}
		if logged := h.logged(); !strings.Contains(logged, "A settled: idle for 10m with the ticket still in progress") {
			t.Errorf("the log should say A settled after the idle grace:\n%s", logged)
		}
	})
}

// countedShows is a tracker that counts its tickets' shows and fails each of a ticket that waits
// on an open question when failAsked is set, as bd does when another bd holds the database.
type countedShows struct {
	*fakeBeads
	failAsked bool
	mu        sync.Mutex
	shows     map[string]int
}

func (b *countedShows) Show(ctx context.Context, id string) (Ticket, error) {
	b.mu.Lock()
	b.shows[id]++
	b.mu.Unlock()
	t, err := b.fakeBeads.Show(ctx, id)
	if err == nil && b.failAsked && OpenQuestion(t) != nil {
		return Ticket{ID: id, Status: "unknown"}, errors.New("bd show " + id + " --json: exit status 1: Error: database is locked")
	}
	return t, err
}

func (b *countedShows) showsOf(id string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.shows[id]
}

// runCounted runs h with its tracker's shows counted, failing those of an asked ticket if failAsked.
func runCounted(h *harness, failAsked bool) (*Loop, *countedShows, int) {
	o := h.loop()
	tickets := &countedShows{fakeBeads: h.beads, failAsked: failAsked, shows: map[string]int{}}
	o.tickets = tickets
	return o, tickets, o.Run(context.Background())
}

// A ticket bd fails to show at its worker's Stop says nothing of the worker: one that asked is not
// settled by it, and is waited on through the idle grace.
func TestFailedShowAtAStopDoesNotSettle(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", asksAndStops(hooks.report))
		start := time.Now()
		o, _, code := runCounted(h, true)
		if code != ExitTool || !strings.HasPrefix(o.Final(), "STATUS_UNREADABLE for A") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if took := time.Since(start); took < idleGrace {
			t.Errorf("the run took %s, less than the idle grace", took)
		}
		if logged := h.logged(); strings.Contains(logged, "A settled: Stop hook") {
			t.Errorf("A should not settle at its Stop, its ticket unread:\n%s", logged)
		}
	})
}

// A worker told to continue as many times as it may be, which ends its next turn with its ticket
// still in progress, is waited on through the idle grace without its ticket shown poll after poll.
func TestTurnOwingWorkIsShownOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", stopsMidTicket(hooks.report), stopsMidTicket(hooks.report), stopsMidTicket(hooks.report))
		o, tickets, code := runCounted(h, false)
		if code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: A still in_progress") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		// Three turn ends and the settled ticket, read once each, and a few more around the start;
		// the idle grace alone is 200 polls.
		if n := tickets.showsOf("A"); n > 10 {
			t.Errorf("A was shown %d times", n)
		}
	})
}
