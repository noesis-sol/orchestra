package dispatch

import (
	"slices"
	"sync"
)

// MergeStage is where a finished ticket is in the merge queue, for the dashboard.
type MergeStage string

// The stages of a finished ticket's merge. A ticket handed back to its worker (Status.Resolving,
// Status.Fixing) is in none of them while its worker works, and joins the queue again after.
const (
	MergeNone    MergeStage = ""         // its worker is at work, or it isn't merging
	MergeQueued  MergeStage = "to merge" // its worker is done; it waits for its turn behind another's merge
	MergeRunning MergeStage = "merging"  // its merge is under way: rebase, check again, fast-forward
)

// mergeQueue is the merge queue: a finished ticket holds it through rebase, check and merge, so Base
// doesn't move while its rebased code is checked. Worktree creation isn't held up. Tickets take their
// turn in the order they join it, so the place in line the dashboard shows (Status.Place) holds.
type mergeQueue struct {
	mu     sync.Mutex
	holder string       // the ticket whose merge is under way; "" when none is
	line   []mergeWaits // the tickets waiting for their turn, next first
}

// mergeWaits is a ticket waiting in the merge queue: turn is closed when it is its turn.
type mergeWaits struct {
	id   string
	turn chan struct{}
}

// mergeTurn is ticket id's use of the merge queue, as a sync.Locker for held: Lock joins the line
// and waits for its turn, Unlock passes the queue on to the next in line. Each shows the stage it
// leaves the tickets in on the dashboard (see showMerge).
type mergeTurn struct {
	o  *Loop
	id string
}

// Lock waits for id's turn to merge, showing it as to merge meanwhile, then as merging.
func (t mergeTurn) Lock() {
	q := &t.o.merges
	q.mu.Lock()
	if q.holder == "" && len(q.line) == 0 {
		q.holder = t.id
		t.o.showMerge(t.id, MergeRunning, 0)
		q.mu.Unlock()
		return
	}
	turn := make(chan struct{})
	q.line = append(q.line, mergeWaits{id: t.id, turn: turn})
	t.o.showMerge(t.id, MergeQueued, len(q.line))
	q.mu.Unlock()
	<-turn
}

// Unlock passes the queue on to the next ticket in line, if any, and moves the others up. id, which
// held it, is no longer shown as merging: it is done with its merge, or handed back to its worker
// (see setHandedBack).
func (t mergeTurn) Unlock() {
	q := &t.o.merges
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.line) == 0 {
		q.holder = ""
		return
	}
	next := q.line[0]
	q.line = slices.Delete(q.line, 0, 1)
	q.holder = next.id
	t.o.showMerge(next.id, MergeRunning, 0)
	for i, w := range q.line {
		t.o.showMerge(w.id, MergeQueued, i+1)
	}
	close(next.turn)
}

// showMerge shows ticket id at stage of its merge on the dashboard, at place in line when it is
// queued (1: next), its worker done with it. A ticket no longer worked on isn't shown. The caller
// holds merges.mu, so the places shown follow the line as it changes.
func (o *Loop) showMerge(id string, stage MergeStage, place int) {
	o.mu.Lock()
	st, ok := o.active[id]
	if ok {
		st.Merge, st.Place = stage, place
		o.active[id] = st
	}
	o.mu.Unlock()
	if !ok {
		return
	}
	activity := st.Activity // a ticket checked again says so (see recheck)
	switch {
	case activity != "":
	case stage == MergeRunning:
		activity = "merging into " + o.cfg.Base + ", rebased and checked again first if it moved on"
	default:
		activity = "waiting for its turn to merge into " + o.cfg.Base
	}
	o.status(Status{Ticket: st.Ticket, Title: st.Title, Tab: st.Tab, Started: st.Started, Agent: StateDone,
		Activity: activity, Merge: stage, Place: place})
}
