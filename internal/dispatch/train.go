package dispatch

import (
	"context"
	"fmt"
	"slices"
	"sync"
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

// mergeTrain is the merge train's state, under its own lock (mu): taken after merges.mu when both are,
// and let go of before anything else is called, the gate's lock aside.
type mergeTrain struct {
	mu sync.Mutex
	// The holder's branch (checkingBr) at the commit its check runs on or passed on (checking; "" when
	// it isn't checked), in the run's context (ctx); the tickets waiting for their first turn, which may
	// be checked ahead of it (ahead), and those that are (trains).
	checking, checkingBr string
	ctx                  context.Context
	ahead                map[string]worker
	trains               map[string]*trainRun
}

// trainRun is a ticket checked ahead of its turn in the merge queue (see startTrains). Its fields from
// orig on are written by its goroutine, under train.mu until done is closed.
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
	cut    bool          // its check was cut short, not failed: the branch ahead didn't merge as checked
	why    string        // what failed on it and how, and where the output is, when its check failed
}

// mayCheckAhead says that w's ticket waits for its first turn in its merge: until it takes the queue,
// it may be checked ahead of its turn (see startTrains). A ticket queueing again after a hand-back to
// its worker isn't, its merge under way.
func (o *Loop) mayCheckAhead(w worker) {
	tr := &o.train
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.ahead == nil {
		tr.ahead = map[string]worker{}
	}
	tr.ahead[w.id] = w
}

// checkStarts says that the ticket holding the queue is checked on its branch br at commit at: the
// tickets next in line may be checked ahead of their turn on top of it. Trains on another commit of
// its branch are stopped, as it no longer merges as they were rebased onto.
func (o *Loop) checkStarts(ctx context.Context, br, at string) {
	q, tr := &o.merges, &o.train
	q.mu.Lock()
	defer q.mu.Unlock()
	tr.mu.Lock()
	if tr.checking != "" && tr.checking != at {
		tr.stopTrains()
	}
	tr.ctx, tr.checking, tr.checkingBr = ctx, at, br
	tr.mu.Unlock()
	o.startTrains()
}

// checkEnds says that the check of the ticket holding the queue passed, or not: the trains on top of
// a branch that fails are stopped, as it doesn't merge as they were rebased onto. Those on one that
// passes go on, as it merges next.
func (o *Loop) checkEnds(passed bool) {
	if passed {
		return
	}
	tr := &o.train
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.checking = ""
	tr.stopTrains()
}

// holderDone says that the ticket holding the queue let go of it: the next holder's check is another.
func (tr *mergeTrain) holderDone() {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.checking = ""
}

// stopTrains cancels every train under way. The caller holds tr.mu.
func (tr *mergeTrain) stopTrains() {
	for _, t := range tr.trains {
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
	for _, t := range o.boardTrains() {
		o.showTrain(t)
	}
}

// boardTrains starts the trains startTrains does, and returns them, for the caller to show. The caller
// holds merges.mu.
func (o *Loop) boardTrains() []*trainRun {
	q, tr, c := &o.merges, &o.train, o.cfg
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if c.MergeTrain <= 0 || c.Check == "" || tr.checking == "" || tr.ctx == nil || tr.ctx.Err() != nil ||
		o.gate.held(gateMachine) {
		return nil
	}
	var started []*trainRun
	behind, onto := tr.checkingBr, tr.checking
	for _, next := range q.line[:min(len(q.line), c.MergeTrain)] {
		w, ok := tr.ahead[next.id]
		if !ok {
			break
		}
		t := tr.trains[next.id]
		if t == nil {
			t = o.startTrain(tr.ctx, w, behind, onto)
			if tr.trains == nil {
				tr.trains = map[string]*trainRun{}
			}
			tr.trains[next.id] = t
			started = append(started, t)
		}
		if t.onto != onto || t.head == "" || t.failed {
			break // on another branch than the one ahead as it is now, or not rebased onto it (yet)
		}
		behind, onto = w.br, t.head
	}
	return started
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
	r := rebaseStop{worker: w, onto: t.onto, head: t.orig, ahead: true}
	output, err := o.setUp(ctx, r)
	setup := err != nil
	if !setup {
		output, err = o.runCheck(ctx, r, head)
	}
	var failure string
	switch {
	case err == nil:
		o.info("  '%s' passes on %s ahead of its turn, on top of %s", c.Check, w.br, t.behind)
	case ctx.Err() == nil:
		failure = fmt.Sprintf("%s on %s on top of it (output is in %s)", o.failedHow(setup, err), w.br, output)
		o.info("  %s on %s ahead of its turn, on top of %s (output is in %s); it is checked again in its turn",
			o.failedHow(setup, err), w.br, t.behind, output)
	}
	o.trainChecked(t, head, err == nil, err != nil && ctx.Err() != nil, failure)
}

// rebaseAhead rebases w's branch onto t.onto in its worktree, holding repoMu, and returns its commit
// after the rebase; "" when it isn't rebased: the run stops, its worktree isn't ready for a rebase (its
// turn says why), it doesn't rebase cleanly (the rebase is aborted), or its commits change nothing on
// top of t.onto (it is put back, and its turn closes it as unchanged). Where git can't tell what that
// turns on, it is not rebased, or put back, and its turn asks again.
func (o *Loop) rebaseAhead(ctx context.Context, w worker, t *trainRun) string {
	c := o.cfg
	keep := context.WithoutCancel(ctx) // once begun, the rebase finishes, or is undone
	o.repoMu.Lock()
	defer o.repoMu.Unlock()
	if ctx.Err() != nil || o.offBranch(keep, w) != "" {
		return ""
	}
	if files, err := o.uncommitted(keep, w.wt); err != nil || len(files) > 0 {
		o.log.Raw("", err)
		return ""
	}
	orig, err := o.checkout.Head(keep, c.Repo, w.br)
	if err != nil || orig == "" {
		o.log.Raw("", err)
		return ""
	}
	o.trainFrom(t, orig)
	out, err := o.merger.Rebase(keep, w.wt, t.onto)
	o.log.Raw(out, err)
	if err != nil {
		if o.rebasing(keep, w.wt) { // one git can't tell is over is aborted: the branch is left as it was
			if err := o.abortRebase(keep, w.wt); err != nil {
				o.info("  %s's rebase onto %s, ahead of its turn, could not be aborted: %v", w.br, t.behind, err)
			}
		}
		o.info("  %s is not checked ahead of its turn: it does not rebase cleanly onto %s", w.br, t.behind)
		return ""
	}
	unchanged, err := o.merger.Unchanged(keep, c.Repo, t.onto, w.br)
	o.log.Raw("", err)
	var head string
	if err == nil && !unchanged {
		head, err = o.checkout.Head(keep, c.Repo, w.br)
		o.log.Raw("", err)
	}
	if head == "" { // changing nothing, or git can't tell
		out, err := o.merger.ResetBranch(keep, w.wt, orig)
		o.log.Raw(out, err)
	}
	return head
}

// trainFrom records the commit t's branch was on before its rebase.
func (o *Loop) trainFrom(t *trainRun, orig string) {
	tr := &o.train
	tr.mu.Lock()
	defer tr.mu.Unlock()
	t.orig = orig
}

// trainRebased records t's branch rebased onto t.onto at head, and starts the train behind it, if any;
// "" says it wasn't rebased, and starts none.
func (o *Loop) trainRebased(t *trainRun, head string) {
	q, tr := &o.merges, &o.train
	q.mu.Lock()
	defer q.mu.Unlock()
	tr.mu.Lock()
	t.head, t.failed = head, head == ""
	tr.mu.Unlock()
	o.showTrain(t)
	if head != "" {
		o.startTrains()
	}
}

// trainChecked records the check on t's branch at head as passed, or not, or cut short, failure saying
// what failed when it did: the trains behind one that failed are stopped, as it is rebased again in its
// turn.
func (o *Loop) trainChecked(t *trainRun, head string, passed, cut bool, failure string) {
	q, tr := &o.merges, &o.train
	q.mu.Lock()
	defer q.mu.Unlock()
	defer o.showTrain(t)
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if passed {
		t.passed = head
		return
	}
	t.failed, t.cut, t.why = true, cut, failure
	behind := false
	for _, w := range q.line {
		if b := tr.trains[w.id]; b != nil && behind {
			b.cancel()
		}
		behind = behind || w.id == t.id
	}
}

// showTrain shows t's ticket, while it waits in line, with how its check ahead of its turn goes (see
// activity). The caller holds merges.mu, not train.mu.
func (o *Loop) showTrain(t *trainRun) {
	q, tr := &o.merges, &o.train
	tr.mu.Lock()
	current := tr.trains[t.id] == t
	tr.mu.Unlock()
	if !current {
		return // its turn came: it shows as merging
	}
	if i := slices.IndexFunc(q.line, func(w mergeWaits) bool { return w.id == t.id }); i >= 0 {
		o.showMerge(t.id, MergeQueued, i+1)
	}
}

// activity is what ticket id, waiting in line, shows of its check ahead of its turn on the dashboard;
// "" when it has none, or none under way or done: it wasn't rebased onto the branch ahead, or its check
// was cut short, as it is rebased and checked in its turn then.
func (tr *mergeTrain) activity(id string) string {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	t := tr.trains[id]
	switch {
	case t == nil:
		return ""
	case t.passed != "":
		return "passed its check ahead of its turn, on top of " + t.behind + ": merges with no further check if " +
			t.behind + " merges as checked"
	case t.failed && t.head != "" && !t.cut:
		return "failed its check ahead of its turn, on top of " + t.behind + ": checked again in its turn"
	case t.failed:
		return ""
	case t.head == "":
		return "rebasing onto " + t.behind + ", to be checked ahead of its turn on top of it as it is checked"
	default:
		return "checked ahead of its turn, on top of " + t.behind + " as it is checked"
	}
}

// takeTrain takes ticket id's train, if it has one, as its turn comes: no train starts for it from now
// on in its merge.
func (o *Loop) takeTrain(id string) *trainRun {
	tr := &o.train
	tr.mu.Lock()
	defer tr.mu.Unlock()
	t := tr.trains[id]
	delete(tr.trains, id)
	delete(tr.ahead, id)
	return t
}

// boardTrain is where t, the train of the ticket now holding the queue, stands it in its turn: while it
// is checked, the tickets behind it may be checked ahead on top of it, as on the check of a ticket in
// its turn (see checkStarts). The caller holds the queue.
func (o *Loop) boardTrain(ctx context.Context, w worker, t *trainRun) {
	q, tr := &o.merges, &o.train
	q.mu.Lock()
	defer q.mu.Unlock()
	tr.mu.Lock()
	if t.head == "" || t.failed {
		tr.mu.Unlock()
		return
	}
	tr.ctx, tr.checking, tr.checkingBr = ctx, t.head, w.br
	tr.mu.Unlock()
	o.startTrains()
}

// board takes the ticket's train in its turn, if it was checked ahead of it (see startTrains), waiting
// for its check to end. When the branch ahead merged as it was checked and the check passed on the
// ticket's branch on top of it, the branch is on top of Base as checked: it merges with no further
// check. Otherwise a branch the train rebased is put back as its worker left it, for the merge to rebase
// and check as any other, and the trains behind it are stopped; the log says which it was, the branch
// ahead not merging as checked or the ticket's own check failing on top of it. It reports false when git could not put
// it back, the ticket set aside then: its branch has the commits of the branch ahead; or git can't tell
// where the branch is, the run stopping then (stop says why). The caller holds its turn, not repo.
func (m *mergeRun) board(ctx context.Context) (bool, *stopReason) {
	o, keep, w := m.o, m.keep, m.w
	t := o.takeTrain(w.id)
	if t == nil {
		return true, nil
	}
	// Where git can't tell where Base is, the branch ahead is taken for one that didn't merge.
	base := m.baseHead()
	merged := base != "" && base == t.onto
	if merged {
		o.boardTrain(ctx, w, t)
	} else {
		t.cancel() // the branch ahead didn't merge as it was rebased onto
	}
	<-t.done
	m.repo.lock()
	head, err := o.head(keep, m.repo, w.br)
	if err != nil { // the branch may have the commits of the branch ahead, which didn't merge
		_, stop := m.gitFailed("where "+w.br+" is, after it was rebased to be checked ahead of its turn", err)
		return false, stop
	}
	if t.passed != "" && head == t.passed && m.baseHead() == t.onto {
		m.repo.unlock()
		m.checked = t.passed
		o.info("  %s was checked ahead of its turn on top of %s, which merged as it was checked: "+
			"merging it with no further check", w.br, t.behind)
		return true, nil
	}
	rebased, err := m.putBack(t, head)
	o.checkEnds(false)
	switch {
	case err != nil:
		m.cantPutBack(t, err)
		return false, nil
	case !rebased:
	case merged && t.why != "":
		o.info("  %s was checked ahead of its turn on top of %s, which merged as it was checked, but %s: "+
			"put back as its worker left it", w.br, t.behind, t.why)
	case merged && t.passed == "":
		o.info("  %s was checked ahead of its turn on top of %s, which merged as it was checked, but its check "+
			"was stopped before it ended: put back as its worker left it", w.br, t.behind)
	default:
		o.info("  %s was checked ahead of its turn on top of %s, which did not merge as it was checked: "+
			"put back as its worker left it", w.br, t.behind)
	}
	return true, nil
}

// leaveTrain stops the ticket's train, if it has one, as the run stops (stop) before its turn comes,
// and puts back a branch it rebased as its worker left it: the ticket is left finished and unmerged,
// for the next run to merge as any other. It returns stop, or the reason to stop when git can't tell
// where the branch is (see gitFailed). The queue isn't held: nothing here moves Base.
func (m *mergeRun) leaveTrain(stop *stopReason) *stopReason {
	o, w := m.o, m.w
	t := o.takeTrain(w.id)
	if t == nil {
		return stop
	}
	t.cancel()
	<-t.done
	m.repo.lock()
	head, err := o.head(m.keep, m.repo, w.br)
	if err != nil { // the branch may have the commits of the branch ahead, which didn't merge
		_, failed := m.gitFailed("where "+w.br+" is, after it was rebased to be checked ahead of its turn", err)
		return failed
	}
	rebased, err := m.putBack(t, head)
	switch {
	case err != nil:
		m.cantPutBack(t, err)
	case rebased:
		o.info("  %s was rebased onto %s to be checked ahead of its turn, but the run stopped before its turn: "+
			"put back as its worker left it", w.br, t.behind)
	}
	return stop
}

// putBack puts the ticket's branch, at head, back as its worker left it, when t rebased it and it hasn't
// moved since, and lets go of repo, which the caller holds. It reports whether the branch was rebased,
// and git's error when it could not put it back.
func (m *mergeRun) putBack(t *trainRun, head string) (bool, error) {
	defer m.repo.unlock()
	if t.head == "" || head != t.head {
		return false, nil
	}
	out, err := m.o.merger.ResetBranch(m.keep, m.w.wt, t.orig)
	m.o.log.Raw(out, err)
	return true, err
}

// cantPutBack sets the ticket aside, git having failed to put its branch back as its worker left it
// (see putBack): it has the commits of the branch ahead.
func (m *mergeRun) cantPutBack(t *trainRun, err error) {
	o, c, w := m.o, m.o.cfg, m.w
	o.leaveFor(m.keep, w.id, asideRebaseFailed, "rebase failed", "", fmt.Sprintf(
		"%s closed, but git could not put %s back as its worker left it, at %s, after it was rebased onto %s "+
			"to be checked ahead of its turn%s; worktree %s and tab %s left for review (git output is in %s)",
		w.id, w.br, short(t.orig), t.behind, because(err), w.wt, w.tab, c.LogPath))
}

// baseHead is the commit Base is on, as board compares it with the one a train was checked on: "" when
// git can't say, logging git's error.
func (m *mergeRun) baseHead() string {
	head, err := m.o.checkout.Head(m.keep, m.o.cfg.Repo, m.o.cfg.Base)
	m.o.log.Raw("", err)
	return head
}
