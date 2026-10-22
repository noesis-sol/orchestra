package dispatch

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"sync"
)

// The ticket book keeps each ticket's place in the run in one record: whether it is in the run like
// any other or kept out of it, and why, with what the run learns of it on the way (what its failed
// check said, what it was set up for, its parent). A record changes only through the book's transitions, each
// of which changes all of it at once, and the questions the run asks of it (what pick skips, what the
// holds wait for, what is busy, what the scope's end line says) are answered from the records by one
// rule each, under the book's own lock.

// disposition is where a ticket stands in the run.
type disposition int

const (
	dispIn      disposition = iota // nothing keeps it out of the run
	dispRunning                    // set aside earlier in the run, and its worker is at it again
	// dispAsked: waiting on a question, its worker left in its tab, which may carry on there once it is
	// answered (see followAsked); or left by the last run, carried over (see loadCarried)
	dispAsked
	dispAside // set aside: deferred, by bd or by its worker, or kept out as bd could not defer it
	// dispLeftOpen: set aside with its worker left in its tab, which may still close it (see leaveOpen);
	// back in the queue since, as the hold for the environment reopens it, it is followed still
	dispLeftOpen
	dispUnmerged    // set aside closed but not merged (see leaveUnmerged)
	dispSetupFailed // its worktree's setup failed, so it waits for a merge or a probe (see leaveSetupFailed)
)

// String names the disposition, for the invariants' errors.
func (d disposition) String() string {
	return [...]string{"in", "running", "asked", "aside", "left open", "unmerged", "setup failed"}[d]
}

// ticketRecord is a ticket's place in the run: its disposition, with what that disposition keeps
// (spot for dispAsked, left for dispLeftOpen, setup for dispSetupFailed), and what is kept across
// dispositions: its place among the tickets set aside in this run, why it is closed but not merged,
// what its last check said if it failed, what its dependencies were set up for, its parent, and
// whether its worker's session was resumed.
type ticketRecord struct {
	disp  disposition
	spot  workerSpot
	left  leftOpen
	setup failedSetup
	// aside: its place among the tickets set aside in this run, from 1; 0 when it isn't set aside. A
	// ticket asked a question in this run is set aside, one carried over with its question is not, and
	// one dispatched again after it was set aside stays set aside until it merges or is back in the
	// queue.
	aside int
	// unmerged: why it is closed but not merged, as this run or an earlier one left it (see
	// leaveUnmerged, loadUnmerged); "" when it isn't. It holds the tickets it blocks until it merges,
	// whatever the disposition.
	unmerged string
	// failedCheck: what its last check in this run said, if it failed (see noteCheckFailed); nil once a
	// later check passes, as the reviewer is told of a failed check only when it is set aside for one.
	failedCheck *checkFail
	// setUpFor: the commit its worktree's dependencies were set up for in this run (see worktreeDeps);
	// "" when they weren't, or its worktree is new.
	setUpFor string
	// setupBroken: the last setup in its worktree in this run failed or was cut short, which may have left
	// its dependencies missing or half installed: the next setup due runs whatever changed (see
	// worktreeDeps.due).
	setupBroken bool
	// parent: its parent, once it is dispatched or left unmerged: until it merges, its parent waits
	// (see parentsOpen).
	parent string
	// resumed: its worker, gone from its tab, had its session resumed in this run (see resumeSession),
	// which happens once.
	resumed bool
}

// empty reports whether r holds nothing: a ticket in the run that nothing in it has marked.
func (r *ticketRecord) empty() bool {
	return r.disp == dispIn && r.aside == 0 && r.unmerged == "" && r.failedCheck == nil && r.setUpFor == "" &&
		!r.setupBroken && r.parent == "" && !r.resumed
}

// ticketBook is each ticket's record, under its own lock. Its zero value is ready to use.
type ticketBook struct {
	mu   sync.Mutex
	recs map[string]*ticketRecord
	// asides is how many times a ticket has been set aside in this run: the next one set aside comes
	// after them.
	asides int
}

// record is ticket id's record, made if it has none. The caller holds mu.
func (b *ticketBook) record(id string) *ticketRecord {
	if b.recs == nil {
		b.recs = map[string]*ticketRecord{}
	}
	r := b.recs[id]
	if r == nil {
		r = &ticketRecord{}
		b.recs[id] = r
	}
	return r
}

// change applies to ticket id's record, made if it has none, then drops the record if it holds
// nothing any more.
func (b *ticketBook) change(id string, to func(r *ticketRecord)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.record(id)
	to(r)
	if r.empty() {
		delete(b.recs, id)
	}
}

// become sets r's disposition to d, dropping what the one before kept.
func (r *ticketRecord) become(d disposition) {
	r.disp, r.spot, r.left, r.setup = d, workerSpot{}, leftOpen{}, failedSetup{}
}

// setAsideIn counts r among the tickets set aside, last unless it is already. The caller holds mu.
func (b *ticketBook) setAsideIn(r *ticketRecord) {
	if r.aside == 0 {
		b.asides++
		r.aside = b.asides
	}
}

// back is the disposition of a ticket kept out no longer: running when it stays set aside (its
// worker at it again), else in.
func (r *ticketRecord) back() disposition {
	if r.aside > 0 {
		return dispRunning
	}
	return dispIn
}

// stays is the disposition of a ticket no longer followed nor waiting: aside when it is set aside,
// else in.
func (r *ticketRecord) stays() disposition {
	if r.aside > 0 {
		return dispAside
	}
	return dispIn
}

// setAside sets ticket id aside: deferred, or kept out as bd could not defer it.
func (b *ticketBook) setAside(id string) {
	b.change(id, func(r *ticketRecord) {
		r.become(dispAside)
		b.setAsideIn(r)
	})
}

// ask sets ticket id aside to wait on a question, its worker left at w.
func (b *ticketBook) ask(id string, w workerSpot) {
	b.change(id, func(r *ticketRecord) {
		r.become(dispAsked)
		r.spot = w
		b.setAsideIn(r)
	})
}

// carry takes on ticket id's worker as the last run left it, at w, as this run's asked tickets are
// (see loadCarried). This run didn't set it aside.
func (b *ticketBook) carry(id string, w workerSpot) {
	b.change(id, func(r *ticketRecord) {
		r.become(dispAsked)
		r.spot = w
	})
}

// answer has ticket id, asked or set aside, running again: its worker carried on in its tab, was
// adopted, or a worker was started on it. Set aside in this run, it stays set aside.
func (b *ticketBook) answer(id string) {
	b.change(id, func(r *ticketRecord) {
		switch r.disp {
		case dispAsked, dispAside, dispLeftOpen:
			r.become(r.back())
		}
	})
}

// dropAsked stops following asked ticket id, its worker gone (see askedDropped): it stays set aside
// if it was.
func (b *ticketBook) dropAsked(id string) {
	b.change(id, func(r *ticketRecord) {
		if r.disp == dispAsked {
			r.become(r.stays())
		}
	})
}

// leaveOpen sets ticket id aside with its worker left in its tab, as lo, to be followed there.
func (b *ticketBook) leaveOpen(id string, lo leftOpen) {
	b.change(id, func(r *ticketRecord) {
		r.become(dispLeftOpen)
		r.left = lo
		b.setAsideIn(r)
	})
}

// unfollow stops following ticket id's worker, left in its tab: it is gone, the ticket is merging, or
// it is dispatched again. The ticket stays set aside if it is.
func (b *ticketBook) unfollow(id string) {
	b.change(id, func(r *ticketRecord) {
		if r.disp == dispLeftOpen {
			r.become(r.stays())
		}
	})
}

// leaveUnmerged sets closed ticket id aside, unmerged for why. One asked stays asked: the run ends,
// and the next run takes its worker up (see leaveAsked).
func (b *ticketBook) leaveUnmerged(id, why string) {
	b.change(id, func(r *ticketRecord) {
		if r.disp != dispAsked {
			r.become(dispUnmerged)
		}
		r.unmerged = why
		b.setAsideIn(r)
	})
}

// noteUnmerged records ticket id as left unmerged by an earlier run, for why, without setting it
// aside (see loadUnmerged).
func (b *ticketBook) noteUnmerged(id, why string) {
	b.change(id, func(r *ticketRecord) { r.unmerged = why })
}

// merged has ticket id merged, or closed with nothing to merge: back in the run, and set aside no
// longer.
func (b *ticketBook) merged(id string) {
	b.change(id, func(r *ticketRecord) {
		r.become(dispIn)
		r.aside, r.unmerged = 0, ""
	})
}

// requeue puts ticket id back in the queue, set aside no longer (see waitForMerge, reopenFailed). A
// worker left in its tab is followed still, until the ticket is dispatched again: it may close it there.
// The run calls it through Loop.requeue, which says so.
func (b *ticketBook) requeue(id string) {
	b.change(id, func(r *ticketRecord) {
		if r.disp != dispLeftOpen {
			r.become(dispIn)
		}
		r.aside = 0
	})
}

// setupFailed keeps ticket id out of the run as its worktree's setup failed, as f; back in the
// queue, it is set aside no longer, while one bd failed to reopen stays set aside if it was. The run
// calls it through Loop.setupFailed, which says so.
func (b *ticketBook) setupFailed(id string, f failedSetup) {
	b.change(id, func(r *ticketRecord) {
		r.become(dispSetupFailed)
		r.setup = f
		if !f.notReopened {
			r.aside = 0
		}
	})
}

// requeue puts ticket id back in the queue (see ticketBook.requeue) and says so, why, with
// EvRequeued: whatever put it there, the dashboard shows it waiting, not working (orchestra-4n6j.6).
func (o *Loop) requeue(id, why string) {
	o.book.requeue(id)
	o.emit(Event{Kind: EvRequeued, Ticket: id, Detail: why})
}

// setupFailed keeps ticket id out of the run as its worktree's setup failed, as f (see
// ticketBook.setupFailed), and says so with EvRequeued, as requeue does.
func (o *Loop) setupFailed(id string, f failedSetup) {
	o.book.setupFailed(id, f)
	why := "its setup failed"
	if f.notReopened {
		why += ", and bd could not reopen it"
	}
	o.emit(Event{Kind: EvRequeued, Ticket: id, Detail: why})
}

// retrySetups lets the tickets whose worktree's setup failed be taken again.
func (b *ticketBook) retrySetups() {
	b.mu.Lock()
	ids := b.idsWhere(func(r *ticketRecord) bool { return r.disp == dispSetupFailed })
	b.mu.Unlock()
	for _, id := range ids {
		b.change(id, func(r *ticketRecord) {
			if r.disp != dispSetupFailed {
				return
			}
			r.become(r.stays())
		})
	}
}

// checkFailed keeps what ticket id's check said as it failed, f, for the reviewer.
func (b *ticketBook) checkFailed(id string, f checkFail) {
	b.change(id, func(r *ticketRecord) { r.failedCheck = &f })
}

// forgetCheck drops what ticket id's last failed check said: a later check of it passed, or it is
// checked again before it merges.
func (b *ticketBook) forgetCheck(id string) {
	b.change(id, func(r *ticketRecord) { r.failedCheck = nil })
}

// setUpSince is the commit ticket id's dependencies were last set up for in this run; before, its
// branch's commit before its rebase, if they weren't, which it keeps as that commit. A setup that fails,
// or is cut short, doesn't leave them as they were (npm ci removes node_modules first): it marks them
// broken instead (see setupBroke), and the next setup runs whatever changed since.
func (b *ticketBook) setUpSince(id, before string) string {
	var from string
	b.change(id, func(r *ticketRecord) {
		if r.setUpFor == "" {
			r.setUpFor = before
		}
		from = r.setUpFor
	})
	return from
}

// setUp records that ticket id's dependencies are set up for commit at, a setup having worked; "" (git
// couldn't say) keeps the commit they were set up for before.
func (b *ticketBook) setUp(id, at string) {
	b.change(id, func(r *ticketRecord) {
		r.setupBroken = false
		if at != "" {
			r.setUpFor = at
		}
	})
}

// setupBroke records that a setup in ticket id's worktree failed or was cut short: its dependencies
// may be missing or half installed, until a setup works there.
func (b *ticketBook) setupBroke(id string) {
	b.change(id, func(r *ticketRecord) { r.setupBroken = true })
}

// brokenSetup reports whether the last setup in ticket id's worktree in this run failed or was cut
// short (see setupBroke).
func (b *ticketBook) brokenSetup(id string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.recs[id]
	return r != nil && r.setupBroken
}

// freshWorktree forgets what ticket id's dependencies were set up for: its worktree is new.
func (b *ticketBook) freshWorktree(id string) {
	b.change(id, func(r *ticketRecord) { r.setUpFor, r.setupBroken = "", false })
}

// setParent records the parent of ticket id, dispatched or left unmerged: until it merges, its parent
// waits. "" records nothing.
func (b *ticketBook) setParent(id, parent string) {
	if parent == "" {
		return
	}
	b.change(id, func(r *ticketRecord) { r.parent = parent })
}

// resumeOnce reports whether ticket id's worker has not had its session resumed in this run yet, and
// records that it has from now on.
func (b *ticketBook) resumeOnce(id string) bool {
	first := false
	b.change(id, func(r *ticketRecord) {
		first, r.resumed = !r.resumed, true
	})
	return first
}

// idsWhere is the tickets whose records match, by ID. The caller holds mu.
func (b *ticketBook) idsWhere(match func(r *ticketRecord) bool) []string {
	var ids []string
	for id, r := range b.recs {
		if match(r) {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// ids is the tickets whose records match, by ID.
func (b *ticketBook) ids(match func(r *ticketRecord) bool) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.idsWhere(match)
}

// matching is the tickets whose records match, by ID, with a copy of each one's record.
func (b *ticketBook) matching(match func(r *ticketRecord) bool) ([]string, []ticketRecord) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids := b.idsWhere(match)
	recs := make([]ticketRecord, len(ids))
	for i, id := range ids {
		recs[i] = *b.recs[id]
	}
	return ids, recs
}

// get is a copy of ticket id's record, and whether it has one.
func (b *ticketBook) get(id string) (ticketRecord, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.recs[id]
	if !ok {
		return ticketRecord{}, false
	}
	return *r, true
}

// asideList is the tickets set aside in this run, in the order they were.
func (b *ticketBook) asideList() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	ids := b.idsWhere(func(r *ticketRecord) bool { return r.aside > 0 })
	slices.SortFunc(ids, func(x, y string) int { return cmp.Compare(b.recs[x].aside, b.recs[y].aside) })
	return ids
}

// countsAside reports whether ticket id is set aside in this run.
func (b *ticketBook) countsAside(id string) bool {
	r, _ := b.get(id)
	return r.aside > 0
}

// endsAside reports whether r counts among the tickets the run's end says it set aside: set aside in
// this run, but for one waiting on a question, which waits on the maintainer instead, unless it was
// closed and left unmerged as the run ended.
func (r *ticketRecord) endsAside() bool {
	return r.aside > 0 && (r.disp != dispAsked || r.unmerged != "")
}

// asideCount is how many tickets the run's end says it set aside (see endsAside).
func (b *ticketBook) asideCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, r := range b.recs {
		if r.endsAside() {
			n++
		}
	}
	return n
}

// keptOut is the tickets the run doesn't take, even if bd lists them as ready (a defer that failed,
// say): those set aside in this run, but for one waiting on a question, which bd keeps out of ready
// until the question is answered, and which then comes back.
func (b *ticketBook) keptOut() []string {
	return b.ids(func(r *ticketRecord) bool { return r.aside > 0 && r.disp != dispAsked })
}

// bookSnapshot is what the holds read of the book, all at one time: the tickets asked, those closed
// but not merged with why, and those left open with the tab their worker was left in.
type bookSnapshot struct {
	asked    map[string]bool
	unmerged map[string]string
	left     map[string]string
}

// snapshot is what the holds read of the book, as it is now.
func (b *ticketBook) snapshot() bookSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := bookSnapshot{asked: map[string]bool{}, unmerged: map[string]string{}, left: map[string]string{}}
	for id, r := range b.recs {
		switch r.disp {
		case dispAsked:
			s.asked[id] = true
		case dispLeftOpen:
			s.left[id] = r.left.w.tab
		}
		if r.unmerged != "" {
			s.unmerged[id] = r.unmerged
		}
	}
	return s
}

// check returns what is wrong with the records, or nil: each disposition keeps what it should, and
// the tickets set aside each have a place of their own.
func (b *ticketBook) check() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	places := map[int]string{}
	for _, id := range slices.Sorted(maps.Keys(b.recs)) {
		r := b.recs[id]
		if err := r.check(id, b.asides); err != nil {
			return err
		}
		if r.aside == 0 {
			continue
		}
		if other, ok := places[r.aside]; ok {
			return fmt.Errorf("%s and %s are both set aside in place %d", other, id, r.aside)
		}
		places[r.aside] = id
	}
	return nil
}

// check returns what is wrong with ticket id's record, or nil; asides is the book's.
func (r ticketRecord) check(id string, asides int) error {
	set := r.aside > 0
	switch {
	case r.aside < 0 || r.aside > asides:
		return fmt.Errorf("%s is set aside in place %d, of %d", id, r.aside, asides)
	case r.empty():
		return fmt.Errorf("%s has an empty record", id)
	case r.disp == dispIn && set:
		return fmt.Errorf("%s is in the run, yet set aside", id)
	case !set && (r.disp == dispRunning || r.disp == dispAside || r.disp == dispUnmerged):
		return fmt.Errorf("%s is %s, yet not set aside", id, r.disp)
	case r.disp == dispUnmerged && r.unmerged == "":
		return fmt.Errorf("%s is unmerged, with no reason", id)
	case r.disp == dispSetupFailed && !r.setup.notReopened && set:
		return fmt.Errorf("%s is back in the queue after its setup failed, yet set aside", id)
	case r.disp != dispAsked && r.spot != (workerSpot{}):
		return fmt.Errorf("%s is %s, yet keeps an asked worker", id, r.disp)
	case r.disp != dispLeftOpen && r.left != (leftOpen{}):
		return fmt.Errorf("%s is %s, yet keeps a worker left open", id, r.disp)
	case r.disp == dispLeftOpen && r.left.w.id != id:
		return fmt.Errorf("%s is left open with %q's worker", id, r.left.w.id)
	case r.disp != dispSetupFailed && r.setup != (failedSetup{}):
		return fmt.Errorf("%s is %s, yet keeps a failed setup", id, r.disp)
	}
	return nil
}
