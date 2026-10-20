package dispatch

import (
	"context"
	"fmt"
)

// The merge train: while the ticket holding the merge queue is checked, the next in line, waiting for
// its first turn in its merge, is rebased onto that ticket's branch as it is checked, in its own
// worktree, and checked there, outside the queue (Config.MergeTrain of them, each on the branch of the
// one before it). When the ticket ahead merges as it was checked, the next one's branch is on top of
// Base exactly as it was checked, and merges in its turn with no second check (see mergeRun.board).
// When it doesn't (its check failed, it went back to its worker, Base moved another way), the next
// one's branch is put back as its worker left it, and rebased and checked in its turn as any other.
// No train starts while the run holds for the machine's cap on workers, as each check competes with
// the workers' for the CPU, and a branch that doesn't rebase cleanly onto the one ahead isn't checked
// ahead: its turn hands back its conflict, if it has one, as it would without the train.

// trainRun is a ticket checked ahead of its turn in the merge queue (see startTrains). Its fields but
// the first are written by its goroutine, under merges.mu until done is closed.
type trainRun struct {
	id     string
	behind string // the branch it was rebased onto, of the ticket ahead of it
	onto   string // the commit it was rebased onto: behind as it is checked
	cancel context.CancelFunc
	done   chan struct{} // closed once it has stopped rebasing and checking
	orig   string        // its branch's commit before the rebase, as its worker left it
	head   string        // its branch's commit after the rebase; "" until it is rebased, or if it wasn't
	passed string        // the commit the check passed on; "" when it didn't, or hasn't yet
	failed bool          // it was not rebased, or its check failed or stopped: none behind it starts
}

// mayCheckAhead says that w's ticket waits for its first turn in its merge: until it takes the queue,
// it may be checked ahead of its turn (see startTrains). A ticket queueing again after a hand-back to
// its worker isn't, its merge under way.
func (o *Loop) mayCheckAhead(w worker) {
	q := &o.merges
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.ahead == nil {
		q.ahead = map[string]worker{}
	}
	q.ahead[w.id] = w
}

// checkStarts says that the ticket holding the queue is checked on its branch br at commit at: the
// tickets next in line may be checked ahead of their turn on top of it. Trains on another commit of
// its branch are stopped, as it no longer merges as they were rebased onto.
func (o *Loop) checkStarts(ctx context.Context, br, at string) {
	q := &o.merges
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.checking != "" && q.checking != at {
		q.stopTrains()
	}
	q.ctx, q.checking, q.checkingBr = ctx, at, br
	o.startTrains()
}

// checkEnds says that the check of the ticket holding the queue passed, or not: the trains on top of
// a branch that fails are stopped, as it doesn't merge as they were rebased onto. Those on one that
// passes go on, as it merges next.
func (o *Loop) checkEnds(passed bool) {
	if passed {
		return
	}
	q := &o.merges
	q.mu.Lock()
	defer q.mu.Unlock()
	q.checking = ""
	q.stopTrains()
}

// stopTrains cancels every train under way. The caller holds merges.mu.
func (q *mergeQueue) stopTrains() {
	for _, t := range q.trains {
		t.cancel()
	}
}

// startTrains starts checking ahead of their turn the first Config.MergeTrain tickets in line that
// wait for their first turn (see mayCheckAhead), each rebased onto the branch of the one ahead of it as
// that one is checked: the ticket holding the queue's, or the train's ahead of it once rebased. It
// starts none while the queue's holder isn't checked, nor as the run stops or holds for the machine's
// cap on workers, and none behind a ticket not checked ahead, or whose train failed. The caller holds
// merges.mu.
func (o *Loop) startTrains() {
	q, c := &o.merges, o.cfg
	if c.MergeTrain <= 0 || c.Check == "" || q.checking == "" || q.ctx == nil || q.ctx.Err() != nil ||
		q.machineFull.Load() {
		return
	}
	behind, onto := q.checkingBr, q.checking
	for _, next := range q.line[:min(len(q.line), c.MergeTrain)] {
		w, ok := q.ahead[next.id]
		if !ok {
			return
		}
		t := q.trains[next.id]
		if t == nil {
			t = o.startTrain(q.ctx, w, behind, onto)
			if q.trains == nil {
				q.trains = map[string]*trainRun{}
			}
			q.trains[next.id] = t
		}
		if t.onto != onto || t.head == "" || t.failed {
			return // on another branch than the one ahead as it is now, or not rebased onto it (yet)
		}
		behind, onto = w.br, t.head
	}
}

// startTrain starts checking w's ticket ahead of its turn, its branch rebased onto onto, the commit of
// branch behind as it is checked, and returns the train under way.
func (o *Loop) startTrain(ctx context.Context, w worker, behind, onto string) *trainRun {
	ctx, cancel := context.WithCancel(ctx)
	t := &trainRun{id: w.id, behind: behind, onto: onto, cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(t.done)
		defer cancel()
		o.runTrain(ctx, w, t)
	}()
	return t
}

// runTrain rebases w's branch onto t.onto and checks it there, setup first when a dependency file
// changed, as its turn would (see checkRebased), and records how it went in t. A check that fails
// says nothing of the ticket: it is checked again in its turn, on the Base it merges onto.
func (o *Loop) runTrain(ctx context.Context, w worker, t *trainRun) {
	c := o.cfg
	head := o.rebaseAhead(ctx, w, t)
	o.trainRebased(t, head)
	if head == "" {
		return
	}
	o.info("  checking %s ahead of its turn to merge, rebased onto %s as it is checked", w.br, t.behind)
	r := rebaseStop{worker: w, onto: t.onto, head: t.orig}
	output, err := o.setUp(ctx, r)
	setup := err != nil
	if !setup {
		output, err = o.runCheck(ctx, r)
	}
	switch {
	case err == nil:
		o.info("  '%s' passes on %s ahead of its turn, on top of %s", c.Check, w.br, t.behind)
	case ctx.Err() == nil:
		o.forgetCheckFailed(w.id) // its turn checks it again, on the Base it merges onto
		o.info("  %s on %s ahead of its turn, on top of %s (output is in %s); it is checked again in its turn",
			o.failedHow(setup, err), w.br, t.behind, output)
	}
	o.trainChecked(t, head, err == nil)
}

// rebaseAhead rebases w's branch onto t.onto in its worktree, holding repoMu, and returns its commit
// after the rebase; "" when it isn't rebased: the run stops, its worktree isn't ready for a rebase (its
// turn says why), it doesn't rebase cleanly (the rebase is aborted), or its commits change nothing on
// top of t.onto (it is put back, and its turn closes it as unchanged).
func (o *Loop) rebaseAhead(ctx context.Context, w worker, t *trainRun) string {
	c := o.cfg
	keep := context.WithoutCancel(ctx) // once begun, the rebase finishes, or is undone
	o.repoMu.Lock()
	defer o.repoMu.Unlock()
	if ctx.Err() != nil || o.offBranch(keep, w) != "" || len(o.uncommitted(keep, w.wt)) > 0 {
		return ""
	}
	orig := o.checkout.Head(keep, c.Repo, w.br)
	if orig == "" {
		return ""
	}
	o.trainFrom(t, orig)
	out, err := o.merger.Rebase(keep, w.wt, t.onto)
	o.log.Raw(out, err)
	if err != nil {
		if o.merger.RebaseInProgress(keep, w.wt) {
			if err := o.abortRebase(keep, w.wt); err != nil {
				o.info("  %s's rebase onto %s, ahead of its turn, could not be aborted: %v", w.br, t.behind, err)
			}
		}
		o.info("  %s is not checked ahead of its turn: it does not rebase cleanly onto %s", w.br, t.behind)
		return ""
	}
	if o.merger.Unchanged(keep, c.Repo, t.onto, w.br) {
		out, err := o.merger.ResetBranch(keep, w.wt, orig)
		o.log.Raw(out, err)
		return ""
	}
	return o.checkout.Head(keep, c.Repo, w.br)
}

// trainFrom records the commit t's branch was on before its rebase.
func (o *Loop) trainFrom(t *trainRun, orig string) {
	q := &o.merges
	q.mu.Lock()
	defer q.mu.Unlock()
	t.orig = orig
}

// trainRebased records t's branch rebased onto t.onto at head, and starts the train behind it, if any;
// "" says it wasn't rebased, and starts none.
func (o *Loop) trainRebased(t *trainRun, head string) {
	q := &o.merges
	q.mu.Lock()
	defer q.mu.Unlock()
	t.head, t.failed = head, head == ""
	if head != "" {
		o.startTrains()
	}
}

// trainChecked records the check on t's branch at head as passed, or not: the trains behind one that
// failed are stopped, as it is rebased again in its turn.
func (o *Loop) trainChecked(t *trainRun, head string, passed bool) {
	q := &o.merges
	q.mu.Lock()
	defer q.mu.Unlock()
	if passed {
		t.passed = head
		return
	}
	t.failed = true
	behind := false
	for _, w := range q.line {
		if b := q.trains[w.id]; b != nil && behind {
			b.cancel()
		}
		behind = behind || w.id == t.id
	}
}

// takeTrain takes ticket id's train, if it has one, as its turn comes: no train starts for it from now
// on in its merge.
func (o *Loop) takeTrain(id string) *trainRun {
	q := &o.merges
	q.mu.Lock()
	defer q.mu.Unlock()
	t := q.trains[id]
	delete(q.trains, id)
	delete(q.ahead, id)
	return t
}

// boardTrain is where t, the train of the ticket now holding the queue, stands it in its turn: while it
// is checked, the tickets behind it may be checked ahead on top of it, as on the check of a ticket in
// its turn (see checkStarts). The caller holds the queue.
func (o *Loop) boardTrain(ctx context.Context, w worker, t *trainRun) {
	q := &o.merges
	q.mu.Lock()
	defer q.mu.Unlock()
	if t.head == "" || t.failed {
		return
	}
	q.ctx, q.checking, q.checkingBr = ctx, t.head, w.br
	o.startTrains()
}

// board takes the ticket's train in its turn, if it was checked ahead of it (see startTrains), waiting
// for its check to end. When the branch ahead merged as it was checked and the check passed on the
// ticket's branch on top of it, the branch is on top of Base as checked: it merges with no further
// check. Otherwise a branch the train rebased is put back as its worker left it, for the merge to rebase
// and check as any other, and the trains behind it are stopped. It reports false when git could not put
// it back, the ticket set aside then: its branch has the commits of the branch ahead. The caller holds
// the queue, not repo.
func (m *mergeRun) board(ctx context.Context) bool {
	o, c, keep, w := m.o, m.o.cfg, m.keep, m.w
	t := o.takeTrain(w.id)
	if t == nil {
		return true
	}
	if o.checkout.Head(keep, c.Repo, c.Base) == t.onto {
		o.boardTrain(ctx, w, t)
	} else {
		t.cancel() // the branch ahead didn't merge as it was rebased onto
	}
	<-t.done
	m.repo.lock()
	head := o.checkout.Head(keep, c.Repo, w.br)
	if t.passed != "" && head == t.passed && o.checkout.Head(keep, c.Repo, c.Base) == t.onto {
		m.repo.unlock()
		m.checked = t.passed
		o.info("  %s was checked ahead of its turn on top of %s, which merged as it was checked: "+
			"merging it with no further check", w.br, t.behind)
		return true
	}
	var err error
	rebased := t.head != "" && head == t.head // and not moved since
	if rebased {
		var out string
		out, err = o.merger.ResetBranch(keep, w.wt, t.orig)
		o.log.Raw(out, err)
	}
	m.repo.unlock()
	o.checkEnds(false)
	switch {
	case err != nil:
		o.leaveFor(keep, w.id, asideRebaseFailed, "rebase failed", "", fmt.Sprintf(
			"%s closed, but git could not put %s back as its worker left it, at %s, after it was rebased onto %s "+
				"to be checked ahead of its turn%s; worktree %s and tab %s left for review (git output is in %s)",
			w.id, w.br, short(t.orig), t.behind, because(err), w.wt, w.tab, c.LogPath))
		return false
	case rebased:
		o.info("  %s was checked ahead of its turn on top of %s, which did not merge as it was checked: "+
			"put back as its worker left it", w.br, t.behind)
	}
	return true
}
