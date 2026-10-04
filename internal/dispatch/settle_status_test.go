package dispatch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// flakyStatus is a tracker whose first fails status reads fail, as bd does when another bd holds
// the database; the reads after them succeed.
type flakyStatus struct {
	*fakeBeads
	mu    sync.Mutex
	fails int
	reads int
}

func (b *flakyStatus) Status(ctx context.Context, id string) (TicketStatus, error) {
	b.mu.Lock()
	b.reads++
	fail := b.reads <= b.fails
	b.mu.Unlock()
	if fail {
		return "unknown", fmt.Errorf("bd show %s --json: exit status 1: Error: database is locked", id)
	}
	return b.fakeBeads.Status(ctx, id)
}

func (b *flakyStatus) readCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reads
}

// runFlaky runs h with its tracker's first fails status reads failing.
func runFlaky(h *harness, fails int) (*Loop, *flakyStatus, int) {
	o := h.loop()
	tickets := &flakyStatus{fakeBeads: h.beads, fails: fails}
	o.tickets = tickets
	return o, tickets, o.Run(context.Background())
}

// A ticket status bd fails to read, once, says nothing about the ticket: a worker whose turn ended
// with its ticket in progress is still told to continue, and the ticket it then closes is merged.
func TestFailedStatusReadStillNudges(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", stopsMidTicket(hooks.report), func(w *fakeWorker) AgentState {
			hooks.report(w.wt, "PreToolUse")
			w.commit("a.txt")
			w.close()
			hooks.report(w.wt, "Stop")
			return "idle"
		})
		o, _, code := runFlaky(h, 1)
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.herdr.pastedTo(); strings.Join(got, ",") != "A" {
			t.Errorf("messages pasted to %v, want one to A", got)
		}
		if !strings.Contains(h.mainLog(), "A: add a.txt") {
			t.Errorf("main:\n%s", h.mainLog())
		}
		if logged := h.logged(); !strings.Contains(logged,
			"cannot read the status of A; still waiting on its worker: bd show A --json") {
			t.Errorf("the failed read should be logged:\n%s", logged)
		}
	})
}

// A worker idle for a moment with its ticket still open, while bd fails once to read the ticket's
// status, has not settled: it isn't deferred, and its close is merged.
func TestFailedStatusReadDoesNotDefer(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A", startsSlowly("a.txt", time.Minute, nil))
		o, _, code := runFlaky(h, 1)
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.of(EvDeferred); len(got) != 0 {
			t.Errorf("deferred:\n%s", strings.Join(got, "\n"))
		}
		if !strings.Contains(h.mainLog(), "A: add a.txt") {
			t.Errorf("main:\n%s", h.mainLog())
		}
	})
}

// A ticket status bd fails to read, read after read, while its worker is idle stops the run after
// a minute's worth of reads, naming the ticket and the error; the first failure alone is logged.
func TestStatusUnreadableWhileIdleStopsTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
		o, tickets, code := runFlaky(h, maxFailedReads+5)
		want := fmt.Sprintf("STATUS_UNREADABLE for A: its status could not be read %d times in a row while its "+
			"worker was idle: bd show A --json: exit status 1: Error: database is locked; stopping", maxFailedReads)
		if code != ExitTool || !strings.HasPrefix(o.Final(), want) {
			t.Fatalf("exit %d, final %q, want %d and %q\n%s", code, o.Final(), ExitTool, want, h.sink.text())
		}
		if n := tickets.readCount(); n != maxFailedReads {
			t.Errorf("read the status %d times, want %d", n, maxFailedReads)
		}
		if n := strings.Count(h.logged(), "cannot read the status of A"); n != 1 {
			t.Errorf("logged the failed read %d times, want once:\n%s", n, h.logged())
		}
		if got := h.herdr.pastedTo(); len(got) != 0 {
			t.Errorf("messages pasted to %v", got)
		}
	})
}
