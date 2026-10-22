package dispatch

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// stageSink keeps each status shown of a ticket's merge stage, as "<ticket> <stage>", with its place
// in line when it waits: "B to merge 1"; and the last status shown of each ticket.
type stageSink struct {
	recordSink
	smu    sync.Mutex
	stages []string
	last   map[string]Status
}

func (s *stageSink) Status(st Status) {
	s.smu.Lock()
	defer s.smu.Unlock()
	if s.last == nil {
		s.last = map[string]Status{}
	}
	s.last[st.Ticket] = st
	switch {
	case st.Gone:
	case st.Resolving:
		s.stages = append(s.stages, st.Ticket+" resolving")
	case st.Fixing:
		s.stages = append(s.stages, st.Ticket+" fixing")
	case st.Merge == MergeQueued:
		s.stages = append(s.stages, fmt.Sprintf("%s %s %d", st.Ticket, st.Merge, st.Place))
	case st.Merge != MergeNone:
		s.stages = append(s.stages, st.Ticket+" "+string(st.Merge))
	}
}

// shown returns the stages shown so far, and forgets them.
func (s *stageSink) shown() []string {
	s.smu.Lock()
	defer s.smu.Unlock()
	l := s.stages
	s.stages = nil
	return l
}

func TestMergeQueueShowsEachTicketsStageAndPlace(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		sink := &stageSink{}
		o := New(Config{Base: "main"}, nil, "", Deps{})
		o.sink = sink
		for _, id := range []string{"A", "B", "C"} {
			o.setActive(Status{Ticket: id, Title: "Ticket " + id, Tab: "tab-" + id, Started: time.Now()})
		}
		turns := map[string]*mergeTurn{}
		var order []string
		var mu sync.Mutex
		var wg sync.WaitGroup
		take := func(id string) {
			wg.Go(func() {
				turn, stop := o.merges.acquire(t.Context(), id)
				if stop != nil {
					t.Errorf("%s: %v", id, stop)
				}
				mu.Lock()
				turns[id] = turn
				order = append(order, id)
				mu.Unlock()
			})
			synctest.Wait() // in line, or holding the queue
		}
		turn := func(id string) *mergeTurn {
			mu.Lock()
			defer mu.Unlock()
			return turns[id]
		}
		expect := func(when string, want ...string) {
			t.Helper()
			if got := sink.shown(); !slices.Equal(got, want) {
				t.Errorf("%s: shown %q, want %q", when, got, want)
			}
		}

		take("A")
		expect("A merges at once with the queue empty", "A merging")
		take("B")
		take("C")
		expect("B and C wait behind A, in the order they came", "B to merge 1", "C to merge 2")

		turn("A").release()
		synctest.Wait()
		expect("A is done: B's turn, and C moves up", "B merging", "C to merge 1")

		// B is handed back to its worker, which lets go of the queue: C's turn.
		o.setHandedBack("B", handFixing)
		turn("B").release()
		synctest.Wait()
		expect("B handed back to fix its check", "B fixing", "C merging")
		if st := o.active["B"]; st.Merge != MergeNone || st.Place != 0 {
			t.Errorf("a ticket handed back should be out of the merge queue, not %q %d", st.Merge, st.Place)
		}

		// Its worker done, B joins the queue again, behind C.
		o.setHandedBack("B", handNone)
		take("B")
		expect("B back in line behind C", "B to merge 1")
		turn("C").release()
		synctest.Wait()
		expect("C is done: B's turn again", "B merging")
		turn("B").release()
		wg.Wait()

		if want := []string{"A", "B", "C", "B"}; !slices.Equal(order, want) {
			t.Errorf("the queue let the tickets in in order %q, want %q, the order they came", order, want)
		}
		if q := &o.merges; q.holder != "" || len(q.line) != 0 {
			t.Errorf("the queue should be empty once all are done: held by %q, %d waiting", q.holder, len(q.line))
		}
		for _, id := range []string{"A", "B", "C"} {
			if st, shown := o.active[id], sink.last[id]; st != shown {
				t.Errorf("%s: the status kept should be the one shown:\nkept  %+v\nshown %+v", id, st, shown)
			}
		}
	})
}

func TestMergeQueueShowsNoTicketNotWorkedOn(t *testing.T) {
	t.Parallel()
	sink := &stageSink{}
	o := New(Config{Base: "main"}, nil, "", Deps{})
	o.sink = sink
	turn, stop := o.merges.acquire(t.Context(), "gone")
	if stop != nil {
		t.Fatal(stop)
	}
	turn.release()
	if got := sink.shown(); len(got) != 0 {
		t.Errorf("a ticket not worked on should not get a box: shown %q", got)
	}
}

func TestMergeShowsTheTicketMergingWhileItsCheckRuns(t *testing.T) {
	t.Parallel()
	f := newMergeFixture(t, "test -f a.txt && test -f b.txt")
	sink := &stageSink{}
	f.orch.sink = sink
	f.orch.setActive(Status{Ticket: "k-1", Title: "Change a", Tab: "tab", Started: time.Now()})
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	f.onMain(t, "b.txt", "b\n")
	if s := f.orch.merge(context.Background(), worker{workerRef: workerRef{wt: wt, tab: "tab"}, id: "k-1", br: "wt/k-1"}); s != nil {
		t.Fatal(s)
	}
	if got := sink.shown(); !slices.Equal(got, []string{"k-1 merging"}) {
		t.Errorf("the ticket should be shown merging through its rebase, check and merge: shown %q", got)
	}
	if ev := sink.text(); !strings.Contains(ev, "passes on the rebased") || !strings.Contains(ev, "k-1 closed") {
		t.Errorf("events:\n%s", ev)
	}
}
