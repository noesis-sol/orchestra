package dispatch

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// lastSink keeps the last status shown of each ticket.
type lastSink struct {
	recordSink
	smu  sync.Mutex
	last map[string]Status
}

func (s *lastSink) Status(st Status) {
	s.smu.Lock()
	defer s.smu.Unlock()
	if s.last == nil {
		s.last = map[string]Status{}
	}
	s.last[st.Ticket] = st
}

// The status the dashboard is shown of a running ticket is the one the loop keeps, whatever changed
// it last: its worker's state read, a hand-back, its place in the merge queue.
func TestStatusShownIsTheOneKept(t *testing.T) {
	t.Parallel()
	sink := &lastSink{}
	o := New(Config{Base: "main"}, nil, "", Deps{Agents: activityAgents{}})
	o.sink = sink
	o.showActive(Status{Ticket: "A", Title: "Ticket A", Tab: "tab-A", Started: time.Now(), Agent: "starting"})
	w := o.newWatcher("wt", Status{Ticket: "A", Tab: "tab-A"})
	kept := func(when string, phase Phase) Status {
		t.Helper()
		o.mu.Lock()
		st := o.active["A"]
		o.mu.Unlock()
		if shown := sink.last["A"]; st != shown {
			t.Errorf("%s: the status kept should be the one shown:\nkept  %+v\nshown %+v", when, st, shown)
		}
		if st.Phase != phase {
			t.Errorf("%s: phase %d, want %d", when, st.Phase, phase)
		}
		return st
	}

	w.report(context.Background(), StateWorking, nil)
	if st := kept("its worker read working", PhaseWorking); st.Agent != StateWorking || st.Activity != editingA ||
		st.Title != "Ticket A" {
		t.Errorf("its worker's read should be kept with the rest: %+v", st)
	}
	w.report(context.Background(), "", errors.New("herdr agent get: server busy"))
	if st := kept("a read failed", PhaseWorking); !st.Unreadable || st.Activity != editingA {
		t.Errorf("a failed read should keep the latest action read: %+v", st)
	}

	o.showMerge("A", MergeQueued, 2)
	if st := kept("waiting to merge", PhaseQueued); st.Merge != MergeQueued || st.Place != 2 ||
		st.Activity != "waiting for its turn to merge into main" {
		t.Errorf("waiting to merge: %+v", st)
	}
	o.setHandedBack("A", handFixing)
	if st := kept("handed back to fix its check", PhaseFixing); !st.Fixing || st.Resolving || st.Merge != MergeNone ||
		st.Place != 0 || st.Activity != "" || st.Title != "Ticket A" {
		t.Errorf("handed back, it should be out of the queue, with none of its words: %+v", st)
	}
	w.report(context.Background(), StateWorking, nil)
	if st := kept("its worker at the fix", PhaseFixing); !st.Fixing || st.Activity != editingA {
		t.Errorf("a read should keep the hand-back: %+v", st)
	}
	o.setHandedBack("A", handNone)
	kept("its fix done", PhaseWorking)
	o.showMerge("A", MergeRunning, 0)
	if st := kept("merging", PhaseMerging); st.Merge != MergeRunning || st.Fixing ||
		st.Activity != "merging into main once checked, rebased onto it first if it moved on" {
		t.Errorf("merging: %+v", st)
	}

	o.clearActive("A")
	sink.last = nil
	w.report(context.Background(), StateWorking, nil)
	o.showMerge("A", MergeRunning, 0)
	if len(sink.last) != 0 {
		t.Errorf("a ticket no longer worked on should not be shown: %+v", sink.last)
	}
}

// A ticket checked again keeps saying so as it merges, whatever its place in the queue.
func TestRecheckedTicketSaysSoAsItMerges(t *testing.T) {
	t.Parallel()
	sink := &lastSink{}
	o := New(Config{Base: "main"}, nil, "", Deps{})
	o.sink = sink
	note := "checking again: main moved on since its check failed"
	o.showActive(Status{Ticket: "A", Started: time.Now(), Activity: note, mergeNote: note})
	for _, stage := range []MergeStage{MergeQueued, MergeQueued, MergeRunning} {
		o.showMerge("A", stage, 1)
		if got := sink.last["A"].Activity; got != note {
			t.Errorf("%s: activity %q, want %q", stage, got, note)
		}
	}
}

// Each phase sets the fields that say the same for the sink, and only those of its own.
func TestPhaseSetsItsFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		phase             Phase
		resolving, fixing bool
		merge             MergeStage
		place             int
	}{
		{PhaseWorking, false, false, MergeNone, 0},
		{PhaseResolving, true, false, MergeNone, 0},
		{PhaseFixing, false, true, MergeNone, 0},
		{PhaseQueued, false, false, MergeQueued, 3},
		{PhaseMerging, false, false, MergeRunning, 0},
	}
	for _, from := range cases {
		for _, c := range cases {
			var st Status
			st.enter(from.phase, 3)
			st.enter(c.phase, 3)
			if st.Phase != c.phase || st.Resolving != c.resolving || st.Fixing != c.fixing || st.Merge != c.merge ||
				st.Place != c.place {
				t.Errorf("from %d to %d: %+v", from.phase, c.phase, st)
			}
			if st.handedBack() != (c.resolving || c.fixing) {
				t.Errorf("phase %d: handed back %t", c.phase, st.handedBack())
			}
		}
	}
}

// activityAgents shows each worker's latest action as editing a.go.
type activityAgents struct{ noAgents }

func (activityAgents) Screen(ctx context.Context, name string, status AgentState) string {
	return editingA
}

// editingA is the action activityAgents show.
const editingA = "⏺ Editing a.go"
