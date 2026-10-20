package dispatch

import (
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A ticket deferred until a later date (bd defer --until) was deferred on purpose: it isn't listed
// as free to run, though its blockers have merged, until that date has come.

// deferredUntilTickets sets b up, as of now, with deferred tickets each blocked by A, closed and
// merged: D with no date, F deferred until a later date, P until one already past.
func deferredUntilTickets(b *fakeBeads, now time.Time) {
	b.add("A", "merged", 1)
	b.set("A", "closed")
	for _, id := range []string{"D", "F", "P"} {
		b.add(id, "deferred", 2)
		b.set(id, "deferred")
		b.link(id, "A", "blocks")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tickets["F"].DeferUntil = now.Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	b.tickets["P"].DeferUntil = now.Add(-time.Hour).UTC().Format(time.RFC3339)
}

func TestDeferredUntilLater(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		until string
		later bool
	}{
		{"", false},
		{"2026-11-03T22:00:00Z", true},
		{"2026-11-03T23:00:00+01:00", true},
		{"2026-10-14T12:00:00Z", false}, // due now
		{"2026-10-04T00:00:00Z", false},
		{"next month", false}, // unreadable: as none
	} {
		if got := (Ticket{DeferUntil: c.until}).DeferredUntilLater(now); got != c.later {
			t.Errorf("DeferUntil %q: later = %v, want %v", c.until, got, c.later)
		}
	}
}

func TestNothingToRunLeavesOutTicketsDeferredUntilLater(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	b := newFakeBeads()
	deferredUntilTickets(b, time.Now())
	n := checkNothing(t, c, b)
	want := []DeferredFree{{ID: "D", Blockers: []string{"A"}}, {ID: "P", Blockers: []string{"A"}}}
	if n == nil || n.FreeErr != nil || !reflect.DeepEqual(n.DeferredFree, want) {
		t.Errorf("got %+v, want deferred free %+v", n, want)
	}
	if n != nil && n.Deferred != 3 {
		t.Errorf("%d deferred, want 3: F is deferred still", n.Deferred)
	}
}

func TestStartLeavesOutTicketsDeferredUntilLater(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		deferredUntilTickets(h.beads, time.Now())
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, id := range []string{"D", "P"} {
			if want := "  DEFERRED_FREE: " + id + ": "; !strings.Contains(ev, want) {
				t.Errorf("the run didn't list %s:\n%s", id, ev)
			}
		}
		if strings.Contains(ev, "DEFERRED_FREE: F:") {
			t.Errorf("the run listed F, deferred until later:\n%s", ev)
		}
	})
}
