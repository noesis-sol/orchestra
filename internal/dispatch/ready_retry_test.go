package dispatch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
)

// flakyQueue is a tracker whose first readyFails reads of bd ready, and first listFails reads of
// bd list that follow one (the queue's, rather than another part of the run's), fail, as bd does
// when a worker's bd holds the database; the reads after them succeed.
type flakyQueue struct {
	*fakeBeads
	mu                    sync.Mutex
	readyFails, listFails int
	readies, lists        int
	afterReady            bool // the last read was bd ready's
}

func (b *flakyQueue) Ready(ctx context.Context, scope string) ([]Ticket, error) {
	b.mu.Lock()
	b.readies++
	fail := b.readies <= b.readyFails
	b.afterReady = !fail
	b.mu.Unlock()
	if fail {
		return nil, fmt.Errorf("bd ready --json: exit status 1: Error: database is locked")
	}
	return b.fakeBeads.Ready(ctx, scope)
}

func (b *flakyQueue) Unclosed(ctx context.Context) ([]Ticket, error) {
	b.mu.Lock()
	queue := b.afterReady
	b.afterReady = false
	if queue {
		b.lists++
	}
	fail := queue && b.lists <= b.listFails
	b.mu.Unlock()
	if fail {
		return nil, fmt.Errorf("bd list --json: exit status 1: Error: database is locked")
	}
	return b.fakeBeads.Unclosed(ctx)
}

func (b *flakyQueue) readyCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.readies
}

// runFlakyQueue runs h with its tracker's first readyFails ready reads and listFails list reads failing.
func runFlakyQueue(h *harness, readyFails, listFails int) (*Loop, *flakyQueue, int) {
	o := h.loop()
	tickets := &flakyQueue{fakeBeads: h.beads, readyFails: readyFails, listFails: listFails}
	o.tickets = tickets
	return o, tickets, o.Run(context.Background())
}

// bd ready failing twice in a row, then bd list failing twice, doesn't stop the run: the queue is
// read again, the ticket is worked and merged, and each failure is logged.
func TestFailedQueueReadsAreRetried(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name                  string
		readyFails, listFails int
		logged                string
	}{
		{"ready", readyTries - 1, 0, "bd ready --json: exit status 1"},
		{"list", 0, readyTries - 1, "bd list --json: exit status 1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.beads.add("A", "first", 1)
				h.worker("A", finishes("a.txt"))
				o, _, code := runFlakyQueue(h, c.readyFails, c.listFails)
				if code != ExitOK {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if !strings.Contains(h.mainLog(), "A: add a.txt") {
					t.Errorf("main:\n%s", h.mainLog())
				}
				logged := h.logged()
				if n := strings.Count(logged, "could not read the queue"); n != readyTries-1 || !strings.Contains(logged, c.logged) {
					t.Errorf("logged %d failed reads, want %d naming %q:\n%s", n, readyTries-1, c.logged, logged)
				}
			})
		})
	}
}

// bd ready failing read after read still stops the run READY_UNREADABLE, after readyTries reads.
func TestQueueUnreadableAfterTriesStopsTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		o, tickets, code := runFlakyQueue(h, 1000, 0)
		want := "READY_UNREADABLE: could not read 'bd ready --json': bd ready --json: exit status 1: Error: database is locked"
		if code != ExitTool || !strings.HasPrefix(o.Final(), want) {
			t.Fatalf("exit %d, final %q, want %d and %q\n%s", code, o.Final(), ExitTool, want, h.sink.text())
		}
		if n := tickets.readyCount(); n != readyTries {
			t.Errorf("read bd ready %d times, want %d", n, readyTries)
		}
	})
}
