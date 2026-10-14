package dispatch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"testing/synctest"
)

// queueCounts is each dispatch's and queue update's counts, "ready/not closed", as the dashboard
// gets them.
func (s *runSink) queueCounts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var l []string
	for _, ev := range s.events {
		switch ev.Kind {
		case EvDispatch:
			l = append(l, fmt.Sprintf("%s %d/%d", ev.Ticket, ev.Queued, ev.Unclosed))
		case EvQueue:
			l = append(l, fmt.Sprintf("queue %d/%d", ev.Queued, ev.Unclosed))
		}
	}
	return l
}

// Beside the ready tickets waiting, the dispatches and queue updates give the tickets not closed:
// open, in progress and deferred alike. Each ticket that closes lowers the count, the last too.
func TestUnclosedCountsTheTicketsNotClosed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.add("D", "deferred", 3)
		h.beads.set("D", StatusDeferred)
		h.beads.add("P", "in progress elsewhere", 3)
		h.beads.set("P", StatusInProgress)
		h.worker("A", finishes("a.txt"))
		h.worker("B", finishes("b.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := []string{"A 1/4", "B 0/3", "queue 0/2"}
		if got := h.sink.queueCounts(); !equal(got, want) {
			t.Errorf("counts %v, want %v", got, want)
		}
	})
}

// A scoped run counts only its scope's tickets not closed: its ticket and its descendants, as it
// reads bd ready. The unrelated U is never among them.
func TestUnclosedCountsOnlyTheScope(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := scoped(newTimedHarness(t), "R")
		h.beads.add("U", "unrelated", 0)
		h.beads.add("R", "root", 2)
		h.beads.sub("R.1", "R", "child", 1)
		h.beads.sub("R.1.1", "R.1", "grandchild", 1)
		h.worker("R.1.1", finishes("r11.txt"))
		h.worker("R.1", finishes("r1.txt"))
		h.worker("R", finishes("r.txt"))
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := []string{"R.1.1 0/3", "R.1 0/2", "R 0/1", "queue 0/0"}
		if got := h.sink.queueCounts(); !equal(got, want) {
			t.Errorf("counts %v, want %v", got, want)
		}
	})
}

// failingDescendants is a tracker whose descendants can't be read.
type failingDescendants struct{ fakeTickets }

func (failingDescendants) Unclosed(context.Context) ([]Ticket, error) {
	return []Ticket{{ID: "R"}, {ID: "R.1", Parent: "R"}, {ID: "U"}}, nil
}

func (failingDescendants) Descendants(context.Context, string) ([]Ticket, error) {
	return nil, errors.New("bd list: unreadable")
}

// A failed read of a scope's descendants keeps the last count, as nothing depends on it; without a
// scope, the count is bd's list of tickets not closed.
func TestUnclosedKeepsTheLastCountOnAFailedRead(t *testing.T) {
	ctx := context.Background()
	r := newRunState(ctx, New(Config{Ticket: "R"}, nil, "", Deps{Tickets: failingDescendants{}}))
	open, _ := r.o.tickets.Unclosed(ctx)
	r.countUnclosed(ctx, open)
	if r.unclosed != -1 {
		t.Errorf("unclosed %d before any read, want -1", r.unclosed)
	}
	r.unclosed = 2
	r.countUnclosed(ctx, open)
	if r.unclosed != 2 {
		t.Errorf("unclosed %d after a failed read, want the last, 2", r.unclosed)
	}
	r = newRunState(ctx, New(Config{}, nil, "", Deps{Tickets: failingDescendants{}}))
	r.countUnclosed(ctx, open)
	if r.unclosed != 3 {
		t.Errorf("unclosed %d without a scope, want 3", r.unclosed)
	}
}

// The event stream's dispatch and queue records give the count; one not yet read is left out.
func TestEventRecordGivesUnclosed(t *testing.T) {
	for _, tc := range []struct {
		ev   Event
		want any // the record's unclosed; nil for none
	}{
		{Event{Kind: EvQueue, Queued: 1, Unclosed: 5}, 5.0},
		{Event{Kind: EvDispatch, Unclosed: 0}, 0.0},
		{Event{Kind: EvQueue, Unclosed: -1}, nil},
		{Event{Kind: EvClosed, Unclosed: 3}, nil},
	} {
		repo := t.TempDir()
		l, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
		if err != nil {
			t.Fatal(err)
		}
		l.Begin(repo, RunStart{})
		l.Record(tc.ev)
		l.End(0)
		if err := l.Close(); err != nil {
			t.Fatal(err)
		}
		recs := records(t, repo)
		if len(recs) != 3 {
			t.Fatalf("records %v", recs)
		}
		if got := recs[1]["unclosed"]; got != tc.want {
			t.Errorf("%s record: unclosed %#v, want %#v", tc.ev.Kind, got, tc.want)
		}
	}
}
