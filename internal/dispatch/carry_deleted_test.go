package dispatch

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
)

// deletedTickets is the tracker, but ticket id was deleted from it: bd show has no trace of it.
// shows counts the reads of it.
type deletedTickets struct {
	Tickets
	id    string
	shows *atomic.Int32
}

func (d deletedTickets) Show(ctx context.Context, id string) (Ticket, error) {
	if id == d.id {
		d.shows.Add(1)
		return Ticket{ID: id, Status: StatusUnknown}, fmt.Errorf("bd show %s: Issue %s not found: %w", id, id, ErrNoTicket)
	}
	return d.Tickets.Show(ctx, id)
}

// orchestra-hu0k: a worker the last run left behind whose ticket was deleted from bd since is gone,
// not unchecked: the next run reads the ticket once, drops the worker, saying why, and saves it no
// more, rather than keeping it for every run after as one bd can't show for a moment.
func TestCarriedOverWorkerOnADeletedTicketIsDropped(t *testing.T) {
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
		if first := saved(t, h); len(first) != 1 {
			t.Fatalf("run 1 left %+v; want A", first)
		}

		o := h.loop()
		var shows atomic.Int32
		o.tickets = deletedTickets{Tickets: o.tickets, id: "A", shows: &shows}
		if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		if want := "  A, carried over from the last run, is dropped: bd has no such ticket"; !strings.Contains(ev, want) {
			t.Errorf("run 2 events lack %q:\n%s", want, ev)
		}
		if strings.Contains(ev, "is kept for the next run") || strings.Contains(ev, "carried over from the last run: A") {
			t.Errorf("A was kept or carried over:\n%s", ev)
		}
		if n := shows.Load(); n != 1 {
			t.Errorf("bd show A ran %d times; want once, as a ticket deleted isn't tried again", n)
		}
		if got := saved(t, h); len(got) != 0 {
			t.Errorf("run 2 saved %+v; want A forgotten", got)
		}
	})
}
