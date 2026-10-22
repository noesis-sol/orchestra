package dispatch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
)

// armedStatus is a tracker whose next status read of a ticket fails once it is armed for it, as bd's
// does when another bd holds the database a moment.
type armedStatus struct {
	*fakeBeads
	mu    sync.Mutex
	armed map[string]bool
}

// arm makes the next status read of ticket id fail.
func (b *armedStatus) arm(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.armed == nil {
		b.armed = map[string]bool{}
	}
	b.armed[id] = true
}

func (b *armedStatus) Status(ctx context.Context, id string) (TicketStatus, error) {
	b.mu.Lock()
	fail := b.armed[id]
	delete(b.armed, id)
	b.mu.Unlock()
	if fail {
		return "unknown", fmt.Errorf("bd show %s --json: exit status 1: Error: database is locked", id)
	}
	return b.fakeBeads.Status(ctx, id)
}

// orchestra-xj6z.9: a bd read that failed as a ticket was taken up to be checked once more lost it its
// only recheck of the run. Here bd fails once to read A's status as B, which fixes A's check, merges:
// the read is asked again after a poll, and A is checked once more and merges.
func TestARecheckWhoseBdReadFailsOnceIsRetried(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = countedCheck(dir, "")
		h.beads.add("A", "first", 1)
		h.beads.add("B", "the fix", 2)
		tickets := &armedStatus{fakeBeads: h.beads}
		h.worker("A", landsMeanwhile("a.txt"))
		h.worker("B", afterSetAside(h, "A", func(w *fakeWorker) AgentState {
			w.write(dir, "fixed", "")
			b := finishes("b.txt")(w)
			tickets.arm("A") // read next as B's merge moves Base on: A's recheck
			return b
		}))
		o := h.loop()
		o.tickets = tickets
		code := o.Run(context.Background())
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"B", "A"}) {
			t.Errorf("merged %v, want B, then A checked again\n%s", got, h.sink.text())
		}
		if n := checkRuns(t, dir, "A"); n != 2 {
			t.Errorf("A's check ran %d times, want 2", n)
		}
		if ev := h.sink.text(); strings.Contains(ev, "A is not checked again") {
			t.Errorf("A's recheck was given up on a read that failed once:\n%s", ev)
		}
		if logged := h.logged(); !strings.Contains(logged, "bd show A --json: exit status 1: Error: database is locked") {
			t.Errorf("the failed read should be logged:\n%s", logged)
		}
	})
}
