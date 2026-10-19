package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// readCounter is a tracker that counts its reads of bd ready.
type readCounter struct {
	*fakeBeads
	mu      sync.Mutex
	readies int
}

func (b *readCounter) Ready(ctx context.Context, scope string) ([]Ticket, error) {
	b.mu.Lock()
	b.readies++
	b.mu.Unlock()
	return b.fakeBeads.Ready(ctx, scope)
}

func (b *readCounter) readyCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.readies
}

// A run held at the machine's cap reads the other runs every machinePoll, so the slot another run's
// worker frees is filled within seconds rather than at the next readyPoll; it reads bd ready only as
// often as it did before while it holds.
func TestMachineHoldWakesWhenSlotFrees(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := sharing(t, h)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(dir, "run-424242-1.json")
		writeOther := func(running int) {
			t.Helper()
			body := fmt.Sprintf(`{"pid":424242,"repo":"/elsewhere","concurrent":2,"started":"1999-12-31T00:00:00Z",`+
				`"running":%d}`, running)
			if err := os.WriteFile(other, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		writeOther(1)
		h.cfg.Concurrency, h.cfg.MachineWorkers = 2, 2
		h.beads.add("A", "ticket A", 1)
		h.beads.add("B", "ticket B", 2)
		h.worker("A", slowly(30*time.Minute, "a.txt"))
		h.worker("B", slowly(time.Minute, "b.txt"))
		o := h.loop()
		o.process = func(int) (time.Time, bool) { return time.Time{}, true }
		tickets := &readCounter{fakeBeads: h.beads}
		o.tickets = tickets

		code := make(chan int, 1)
		go func() { code <- o.Run(context.Background()) }()
		time.Sleep(5*time.Minute + 7*time.Second) // off the run's polls
		synctest.Wait()
		heldReads := tickets.readyCount()
		freed := time.Now()
		writeOther(0)
		if c := <-code; c != ExitOK {
			t.Fatalf("exit %d\n%s", c, h.sink.text())
		}

		s := h.sink
		if got := s.of(EvMachine); len(got) != 1 {
			t.Errorf("holds %q, want one", got)
		}
		b := s.dispatchedAt("B")
		if b.Before(freed) || b.Sub(freed) > machinePoll {
			t.Errorf("B dispatched %v after the slot freed, want within %v\n%s", b.Sub(freed), machinePoll, s.text())
		}
		// Five minutes held: a read as the run starts each ticket and as it holds, one each readyPoll.
		if limit := 2 + int(5*time.Minute/readyPoll) + 2; heldReads > limit {
			t.Errorf("%d reads of bd ready while held, want at most %d: one each readyPoll", heldReads, limit)
		}
	})
}
