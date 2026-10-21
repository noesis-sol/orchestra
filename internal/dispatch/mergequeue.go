package dispatch

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"
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
// turn in the order they join it, so the place in line the dashboard shows (Status.Place) holds. A
// ticket takes its turn with acquire, and lets go of it with release (see mergeTurn).
type mergeQueue struct {
	o      *Loop
	mu     sync.Mutex   // taken before train.mu, when both are
	holder string       // the ticket whose merge is under way; "" when none is
	line   []mergeWaits // the tickets waiting for their turn, next first
}

// mergeWaits is a ticket waiting in the merge queue: turn is closed when it is its turn.
type mergeWaits struct {
	id   string
	turn chan struct{}
}

// mergeTurn is ticket id's turn in the merge queue, from acquire, which waits for it, to release, which
// passes the queue on to the next in line. A hand-back to the ticket's worker lets go of it while the
// worker works, and retake joins the line again. Each shows the stage it leaves the tickets in on the
// dashboard (see showMerge). Only the ticket's merge uses its turn.
type mergeTurn struct {
	q    *mergeQueue
	id   string
	held bool // id holds the queue: from acquire or retake to release
}

// acquire waits for ticket id's turn to merge, showing it as to merge meanwhile, then as merging, and
// returns the turn, held. As the run stops (ctx is done) it gives up waiting, leaving the line without
// the queue, and returns errInterrupted: a merge must not begin after a stop.
func (q *mergeQueue) acquire(ctx context.Context, id string) (*mergeTurn, *stopReason) {
	t := &mergeTurn{q: q, id: id}
	if s := t.retake(ctx); s != nil {
		return nil, s
	}
	return t, nil
}

// retake waits for the ticket's turn again, after a release (see acquire), and holds the queue; or
// returns errInterrupted, the queue not held, as the run stops.
func (t *mergeTurn) retake(ctx context.Context) *stopReason {
	q := t.q
	if ctx.Err() != nil {
		return errInterrupted
	}
	q.mu.Lock()
	if q.holder == "" && len(q.line) == 0 {
		q.holder, t.held = t.id, true
		q.o.showMerge(t.id, MergeRunning, 0)
		q.mu.Unlock()
		return nil
	}
	turn := make(chan struct{})
	q.line = append(q.line, mergeWaits{id: t.id, turn: turn})
	q.o.showMerge(t.id, MergeQueued, len(q.line))
	q.o.startTrains() // it may be checked ahead of its turn, as the holder is checked
	q.mu.Unlock()
	select {
	case <-turn:
	case <-ctx.Done():
	}
	if ctx.Err() == nil {
		t.held = true
		return nil
	}
	if !q.leaveLine(t.id) { // its turn came as the run stopped: the next in line has it
		t.held = true
		t.release()
	}
	return errInterrupted
}

// leaveLine takes id out of the line as the run stops, moves those behind it up and reports true;
// or false when id isn't in it, its turn having come meanwhile, as the run stopped: it holds the queue
// then.
func (q *mergeQueue) leaveLine(id string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	i := slices.IndexFunc(q.line, func(w mergeWaits) bool { return w.id == id })
	if i < 0 {
		return false
	}
	q.line = slices.Delete(q.line, i, i+1)
	for j := i; j < len(q.line); j++ {
		q.o.showMerge(q.line[j].id, MergeQueued, j+1)
	}
	return true
}

// release passes the queue on to the next ticket in line, if any, and moves the others up. t's ticket,
// which held it, is no longer shown as merging: it is done with its merge, or handed back to its
// worker (see setHandedBack). A turn not held, or nil, has nothing to release: released already, or
// not taken again after a hand-back as the run stopped.
func (t *mergeTurn) release() {
	if t == nil || !t.held {
		return
	}
	t.held = false
	q := t.q
	q.mu.Lock()
	defer q.mu.Unlock()
	q.passOn()
}

// passOn gives the queue to the next ticket in line, if any, its holder done with it, and moves the
// others up. The caller holds q.mu.
func (q *mergeQueue) passOn() {
	q.o.train.holderDone() // the next holder's check is another
	if len(q.line) == 0 {
		q.holder = ""
		return
	}
	next := q.line[0]
	q.line = slices.Delete(q.line, 0, 1)
	q.holder = next.id
	q.o.showMerge(next.id, MergeRunning, 0)
	for i, w := range q.line {
		q.o.showMerge(w.id, MergeQueued, i+1)
	}
	close(next.turn)
}

// showMerge shows ticket id at stage of its merge on the dashboard, at place in line when it is
// queued (1: next), its worker done with it, and how its check ahead of its turn goes, if it has one
// (see mergeTrain.activity). A ticket no longer worked on isn't shown, and one handed back to its worker
// stays so. The caller holds merges.mu, so the places shown follow the line as it changes.
func (o *Loop) showMerge(id string, stage MergeStage, place int) {
	phase := PhaseQueued
	if stage == MergeRunning {
		phase = PhaseMerging
	}
	var train string
	if stage == MergeQueued {
		train = o.train.activity(id) // one checked ahead of its turn says how it goes
	}
	o.updateActive(id, func(st *Status) {
		if st.handedBack() {
			// Its worker has it until setHandedBack says it is done: a stop retakes the queue for it
			// mid-hand-back, its rebase or failing check left to the worker.
			return
		}
		st.enter(phase, place)
		o.clocks.enter(id, phase, time.Now())
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
