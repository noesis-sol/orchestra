package dispatch

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"sync/atomic"
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
	// The merge train (see train.go): the holder's branch (checkingBr) at the commit its check runs on
	// or passed on (checking; "" when it isn't checked), in the run's context (ctx); the tickets waiting
	// for their first turn, which may be checked ahead of it (ahead), and those that are (trains).
	checking, checkingBr string
	ctx                  context.Context
	ahead                map[string]worker
	trains               map[string]*trainRun
	// machineFull: the run holds for Config.MachineWorkers (see runState.machineFull), so no train starts.
	machineFull atomic.Bool
	// gone: the tickets whose workers have settled and that have gone to merge, until their goroutines
	// return, a hand-back to their workers included; up to Concurrency of them hold no slot (see
	// runState.slotsTaken). Run hears of each on joined (buffered 1), as a slot may be free.
	gone   map[string]bool
	joined chan struct{}
}

// goToMerge says that ticket id's worker has settled and the ticket goes to merge: Run no longer
// counts it among the workers at work.
func (o *Loop) goToMerge(id string) {
	q := &o.merges
	q.mu.Lock()
	if q.gone == nil {
		q.gone = map[string]bool{}
	}
	q.gone[id] = true
	q.mu.Unlock()
	select {
	case q.joined <- struct{}{}:
	default: // Run has yet to hear an earlier one, and counts this one with it
	}
}

// doneMerging says that ticket id's goroutine has returned: merged, set aside or stopped.
func (o *Loop) doneMerging(id string) {
	q := &o.merges
	q.mu.Lock()
	delete(q.gone, id)
	q.mu.Unlock()
}

// goneToMerge is how many of the tickets in flight have gone to merge (see goToMerge).
func (o *Loop) goneToMerge(inflight map[string]bool) int {
	q := &o.merges
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for id := range q.gone {
		if inflight[id] {
			n++
		}
	}
	return n
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
	o    *Loop
	id   string
	stop <-chan struct{} // closed as the run stops (its context's Done): Lock gives up waiting then
}

// Lock waits for id's turn to merge, showing it as to merge meanwhile, then as merging. It gives up
// waiting as the run stops, leaving the line without the queue, for the caller to see the stop and
// return: a merge must not begin after it (see mergeRun.pass).
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
	t.o.startTrains() // it may be checked ahead of its turn, as the holder is checked
	q.mu.Unlock()
	select {
	case <-turn:
	case <-t.stop:
		t.leaveLine()
	}
}

// leaveLine takes id out of the line as the run stops, and moves those behind it up; unless its turn
// came meanwhile, as the run stopped: it holds the queue then.
func (t mergeTurn) leaveLine() {
	q := &t.o.merges
	q.mu.Lock()
	defer q.mu.Unlock()
	i := slices.IndexFunc(q.line, func(w mergeWaits) bool { return w.id == t.id })
	if i < 0 {
		return
	}
	q.line = slices.Delete(q.line, i, i+1)
	for j := i; j < len(q.line); j++ {
		t.o.showMerge(q.line[j].id, MergeQueued, j+1)
	}
}

// Unlock passes the queue on to the next ticket in line, if any, and moves the others up. id, which
// held it, is no longer shown as merging: it is done with its merge, or handed back to its worker
// (see setHandedBack).
func (t mergeTurn) Unlock() {
	q := &t.o.merges
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.holder != t.id {
		return // it gave up waiting for its turn as the run stopped (see Lock)
	}
	q.checking = "" // the next holder's check is another
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
// queued (1: next), its worker done with it, and how its check ahead of its turn goes, if it has one
// (see trainActivity). A ticket no longer worked on isn't shown, and one handed back to its worker
// stays so. The caller holds merges.mu, so the places shown follow the line as it changes.
func (o *Loop) showMerge(id string, stage MergeStage, place int) {
	phase := PhaseQueued
	if stage == MergeRunning {
		phase = PhaseMerging
	}
	var train string
	if stage == MergeQueued {
		train = o.merges.trainActivity(id) // one checked ahead of its turn says how it goes
	}
	o.updateActive(id, func(st *Status) {
		if st.handedBack() {
			// Its worker has it until setHandedBack says it is done: a stop retakes the queue for it
			// mid-hand-back, its rebase or failing check left to the worker.
			return
		}
		st.enter(phase, place)
		st.Agent, st.Doing, st.Permission, st.Unreadable = StateDone, "", false, false
		// A ticket checked again says so (see recheck).
		st.Activity = cmp.Or(train, st.mergeNote)
		switch {
		case st.Activity != "":
		case stage == MergeRunning:
			st.Activity = "merging into " + o.cfg.Base + " once checked, rebased onto it first if it moved on"
		default:
			st.Activity = "waiting for its turn to merge into " + o.cfg.Base
		}
	})
}
