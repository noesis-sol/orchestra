package dispatch

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
)

// flakyConclude is a tracker whose first fails shows of a closed ticket fail, as bd does when
// another bd holds the database: those are conclude's, once the worker has closed its ticket.
type flakyConclude struct {
	*fakeBeads
	mu    sync.Mutex
	fails int
	shows int
}

func (b *flakyConclude) Show(ctx context.Context, id string) (Ticket, error) {
	t, err := b.fakeBeads.Show(ctx, id)
	if err != nil || t.Status != StatusClosed {
		return t, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.shows++
	if b.shows <= b.fails {
		return Ticket{ID: id, Status: "unknown"}, errors.New("bd show " + id + " --json: exit status 1: Error: database is locked")
	}
	return t, nil
}

func (b *flakyConclude) showCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.shows
}

// runFlakyConclude runs h with its tracker's first fails shows of a closed ticket failing.
func runFlakyConclude(h *harness, fails int) (*Loop, *flakyConclude, int) {
	o := h.loop()
	tickets := &flakyConclude{fakeBeads: h.beads, fails: fails}
	o.tickets = tickets
	return o, tickets, o.Run(context.Background())
}

// A ticket bd fails to show once, as its worker settles, is read again: its close is merged.
func TestConcludeRetriesAFailedShow(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.commit("a.txt")
			w.close()
			return StateGone // Claude Code quit after closing, say
		})
		o, tickets, code := runFlakyConclude(h, 1)
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if n := tickets.showCount(); n < 2 {
			t.Errorf("showed the closed ticket %d times, want it read again", n)
		}
		if !strings.Contains(h.mainLog(), "A: add a.txt") {
			t.Errorf("main:\n%s", h.mainLog())
		}
	})
}

// A ticket bd fails to show, try after try, as its worker settles still stops the run.
func TestConcludeStopsWhenShowKeepsFailing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		o, tickets, code := runFlakyConclude(h, 1000)
		want := "STATUS_UNREADABLE for A: bd show A --json: exit status 1: Error: database is locked; stopping"
		if code != ExitTool || !strings.HasPrefix(o.Final(), want) {
			t.Fatalf("exit %d, final %q, want %d and %q\n%s", code, o.Final(), ExitTool, want, h.sink.text())
		}
		if n := tickets.showCount(); n != 3 {
			t.Errorf("showed the closed ticket %d times, want 3", n)
		}
		if strings.Contains(h.mainLog(), "A: add a.txt") {
			t.Errorf("A merged unread:\n%s", h.mainLog())
		}
	})
}
