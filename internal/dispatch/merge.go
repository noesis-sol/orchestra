package dispatch

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// finish merges w's closed ticket, or leaves it for review without a commit naming it or with
// uncommitted changes. One with no commits of its own and a clean worktree has nothing to merge, and
// is cleaned up as after a merge (see closedUnchanged).
func (o *Loop) finish(ctx context.Context, w worker) *stopReason {
	c := o.cfg
	id, br, wt, tab := w.id, w.br, w.wt, w.tab
	keep := context.WithoutCancel(ctx) // a read cut short would look like no commit
	// Its commits may be on the branch its worktree is on instead, and a rebase would rebase that.
	if on := o.offBranch(keep, w); on != "" {
		o.leaveOffBranch(keep, w, on)
		return nil
	}
	commit := o.merger.CommitNaming(keep, c.Repo, c.Base, br, id)
	own := o.merger.CountCommits(keep, c.Repo, c.Base+".."+br)
	switch closedOutcomeOf(commit, own, o.checkout.DirtyWorktree(keep, wt) != "") {
	case closedNoChange:
		repo := &lockGuard{mu: &o.repoMu}
		defer repo.release() // let go of even if the cleanup panics
		o.closedUnchanged(keep, w, fmt.Sprintf("%s has no commits beyond %s and its worktree is clean", br, c.Base),
			repo)
	case closedNoCommit:
		o.leaveFor(keep, id, asideNoCommit, "closed without a commit", "", fmt.Sprintf(
			"no commit on %s names %s; worktree %s and tab %s left for review", br, id, wt, tab))
	case closedDirty:
		o.leaveFor(keep, id, asideUncommitted, "closed with uncommitted changes", "", fmt.Sprintf(
			"%s closed (%s) but %s has uncommitted changes; worktree and tab %s left for review", id, commit, wt, tab))
	case closedMerge:
		return o.merge(ctx, w)
	}
	return nil
}

// merge brings w's finished ticket's branch onto Base, one ticket at a time, once the project's check
// command passes on it: its worker checked its own change, and the merge check tests everything, so
// no ticket reaches Base unchecked. When other tickets merged while it ran, the branch is rebased
// first, and checked as rebased; one on top of Base already is checked as it is (see checkOnTop). A
// rebase that stops on conflicts is handed back to the ticket's worker to resolve, when it can be (see
// whyNotHandBack), without holding up the merge queue meanwhile, and so is a check that fails on the
// branch, to fix (see checkRebased); what the worker leaves is checked once, rebased first when Base
// moved on meanwhile. A conflict left unresolved or a failing check leaves the ticket for review and the
// run goes on; a failing check is tried once more after Base moves on (see recheck).
// A branch whose commits, taken together, change nothing, before its rebase or after (by git, or by
// its worker skipping those Base has already), has nothing to merge and is cleaned up as such (see
// closedUnchanged).
func (o *Loop) merge(ctx context.Context, w worker) *stopReason {
	c := o.cfg
	o.goToMerge(w.id) // its slot is free for the next ticket while it waits for its turn and merges
	defer o.markFinishing(w.id, finishMerge)()
	m := &mergeRun{
		o: o, w: w,
		// Once begun, rebasing, merging and the notes on it finish though Ctrl+C comes, so the
		// repository isn't left half merged; only the check and a worker resolving conflicts stop.
		keep: context.WithoutCancel(ctx),
		// let go of even if the merge panics
		repo: &lockGuard{mu: &o.repoMu}, queue: &lockGuard{mu: mergeTurn{o: o, id: w.id, stop: ctx.Done()}},
	}
	defer m.repo.release()
	o.mayCheckAhead(w) // checked ahead of its turn while the ticket before it is (see train.go)
	m.queue.lock()
	defer m.queue.release() // held on every return but a stop's; a hand-back lets go of it and takes it again
	if !m.board(ctx) {
		return nil // set aside
	}
	stop, done := mergePasses(func(rebase bool) (mergeStep, *stopReason) { return m.pass(ctx, rebase) })
	if !done {
		o.leaveFor(m.keep, w.id, asideConflict, c.Base+" kept changing", c.Base+" kept changing", fmt.Sprintf(
			"%s closed, but %s kept changing while its checks ran (commits made by hand?); "+
				"worktree %s and tab %s left for review", w.id, c.Base, w.wt, w.tab))
	}
	return stop
}

// maxMergePasses is how many passes of a merge may find Base moved, and rebase onto it, while the
// ticket holds the merge queue. Only merges move Base during a run, and they queue too; a second pass
// covers a commit made by hand while the checks ran. A pass after a worker worked on the branch, the
// queue let go of meanwhile, doesn't count (see mergeAgainAfterWorker). After the last of them, one
// more pass merges the branch if Base stayed put while it was checked, and rebases no more.
const maxMergePasses = 3

// mergeStep is what a pass of a merge (see mergeRun.pass) leaves to do.
type mergeStep int

const (
	mergeDone             mergeStep = iota // merged, cleaned up, set aside, or stopped: the merge is over
	mergeAgain                             // rebased and checked while holding the queue: merge, or rebase again
	mergeAgainAfterWorker                  // its worker resolved or fixed the branch: Base may have moved meanwhile
	mergeBaseMoved                         // Base moved, on a pass that may not rebase: it kept changing
)

// mergePasses runs pass until it says the merge is done, returning its reason to stop, if any, and
// true; or false when Base moved once more after maxMergePasses passes had rebased onto it (see
// maxMergePasses). pass may rebase until those passes have run out.
func mergePasses(pass func(rebase bool) (mergeStep, *stopReason)) (*stopReason, bool) {
	for counted := 0; ; {
		step, stop := pass(counted < maxMergePasses)
		switch step {
		case mergeDone:
			return stop, true
		case mergeBaseMoved:
			return nil, false
		case mergeAgain:
			counted++
		case mergeAgainAfterWorker: // not counted: Base moved while the queue was let go of, as it may
		}
	}
}

// mergeRun is the merge of a finished ticket under way: its worker, the context its git work runs in
// to the end (keep), the locks it takes and the hand-backs to its worker so far.
//
// The locks: the merge queue is held throughout, but while its worker works on a hand-back. repo, the
// main repository's (repoMu), is taken at the start of each pass and passed on from step to step: each
// step either hands it to the next or lets go of it before it returns, so a pass always ends with it
// let go of. A step that sets the ticket aside, or closes it, lets go of it first.
type mergeRun struct {
	o           *Loop
	w           worker
	keep        context.Context
	repo, queue *lockGuard
	handedBack  int    // its rebase's conflicts handed back to its worker to resolve
	fixes       fixes  // its failing check handed back to its worker to fix
	checked     string // the branch's commit the check last passed on, on top of Base: it merges as it is
}

// pass merges the ticket's branch if it is on top of Base and checked there, or checks it there
// first, or rebases it onto Base and checks it when it may (rebase), and says what is left to do.
// Once the run has stopped it does none of that, the ticket left finished and unmerged for the next
// run: what follows a stop, the queue's turn included (see mergeTurn.Lock), mustn't rebase the branch.
func (m *mergeRun) pass(ctx context.Context, rebase bool) (mergeStep, *stopReason) {
	c := m.o.cfg
	if ctx.Err() != nil {
		return mergeDone, errInterrupted
	}
	m.repo.lock()
	// Its commits may undo each other, as when its worker found the change on Base already and
	// reverted its own: rebasing them would replay the revert onto Base's change.
	if m.dropUnchanged(c.Base, fmt.Sprintf("the commits on %s, taken together, change nothing", m.w.br)) {
		return mergeDone, nil
	}
	if m.o.merger.IsAncestor(m.keep, c.Repo, c.Base, m.w.br) {
		if c.Check == "" || m.isChecked() {
			return mergeDone, m.fastForward()
		}
		if rebase {
			return m.checkOnTop(ctx)
		}
	}
	if !rebase {
		m.repo.unlock()
		return mergeBaseMoved, nil
	}
	r, conflicted, done := m.rebase()
	switch {
	case done:
		return mergeDone, nil
	case conflicted:
		return m.resolveConflict(ctx, r)
	}
	return m.checkClean(ctx, r)
}

// dropUnchanged closes the ticket as unchanged, for why (see closedUnchanged), when the commits on its
// branch beyond onto, taken together, change nothing, and says whether it did. It is called with repo
// held, and lets go of it only when it did.
func (m *mergeRun) dropUnchanged(onto, why string) bool {
	if !m.o.merger.Unchanged(m.keep, m.o.cfg.Repo, onto, m.w.br) {
		return false
	}
	m.o.dropBranch(m.keep, m.w.wt)
	m.repo.unlock()
	m.o.closedUnchanged(m.keep, m.w, why, m.repo)
	return true
}

// isChecked reports whether the check has passed on the ticket's branch as it is now, in this merge:
// a fast-forward then brings onto Base the very code checked. It is called with repo held.
func (m *mergeRun) isChecked() bool {
	at := m.o.checkout.Head(m.keep, m.o.cfg.Repo, m.w.br)
	return at != "" && at == m.checked
}

// checkOnTop checks the ticket's branch, on top of Base already as its worker left it, before it
// merges, as checkClean does a rebased one: a failure is handed back to its worker to fix or sets the
// ticket aside. It is called with repo held.
func (m *mergeRun) checkOnTop(ctx context.Context) (mergeStep, *stopReason) {
	o, c, keep, w := m.o, m.o.cfg, m.keep, m.w
	r := rebaseStop{worker: w, onto: o.checkout.Head(keep, c.Repo, c.Base), head: o.checkout.Head(keep, c.Repo, w.br),
		onTop: true}
	m.repo.unlock()
	o.info("  %s is on top of %s already: checking it with '%s' before it merges", w.br, c.Base, c.Check)
	res, stop := o.checkRebased(ctx, m.repo, m.queue, r, &m.fixes, false)
	return m.afterCheck(r, res, stop)
}

// afterCheck says what is left to do once the check on r's branch has come out as res, or stopped
// the run (stop), setting the ticket aside when it failed. repo is not held.
func (m *mergeRun) afterCheck(r rebaseStop, res checked, stop *stopReason) (mergeStep, *stopReason) {
	switch {
	case stop != nil:
		return mergeDone, stop
	case res.err != nil:
		m.o.checksFailed(m.keep, r, res)
		return mergeDone, nil
	case res.moved:
		return mergeAgainAfterWorker, nil // fixed while Base moved on: rebase onto it, and check it there
	}
	m.checked = res.at
	if len(res.tries) > 0 {
		return mergeAgainAfterWorker, nil // fixed: the queue was let go of while its worker worked
	}
	// Lock again and merge; if Base moved once more meanwhile, rebase and check again.
	return mergeAgain, nil
}

// fastForward merges the ticket's branch, on top of Base, into it and cleans up after it, or returns
// the reason to stop when the main checkout isn't ready for it or git won't. It is called with repo
// held.
func (m *mergeRun) fastForward() *stopReason {
	o, c, keep := m.o, m.o.cfg, m.keep
	id, br, wt, tab := m.w.id, m.w.br, m.w.wt, m.w.tab
	left := fmt.Sprintf(" before merging %s; worktree %s and tab %s left for review", br, wt, tab)
	if s := o.checkoutUnready(keep, left, alreadyLocked{}); s != nil {
		m.repo.unlock()
		o.leaveUnmerged(keep, id, string(s.kind)) // DIRTY_TREE or GIT_FAILED
		return s
	}
	commit := o.merger.CommitNaming(keep, c.Repo, c.Base, br, id)
	before := o.checkout.Head(keep, c.Repo, c.Base)
	out, err := o.merger.FastForward(keep, c.Repo, br)
	o.log.Raw(out, err)
	if err != nil {
		s := o.notFastForwarded(keep, m.w, err)
		m.repo.unlock()
		o.leaveUnmerged(keep, id, string(s.kind)) // DIRTY_TREE or MERGE_FAILED
		return s
	}
	o.clearUnmerged(keep, id)
	o.countMerge()
	// What it brought may be what a setup that failed needed, or a new setup (see leaveSetupFailed).
	o.retrySetups()
	o.takeSettings(keep, id, before)
	cleaned := o.removeWorktree(keep, id, wt, br)
	m.repo.unlock()
	hash, _, _ := strings.Cut(commit, " ")
	title := o.titleOf(id)
	if cleaned {
		removed := o.closeWorkerTab(keep, id, tab)
		o.emit(Event{Kind: EvClosed, Ticket: id, Title: title, Detail: hash + " merged into " + c.Base, Text: fmt.Sprintf(
			"  %s closed (%s); merged into %s, %s", id, commit, c.Base, removed)})
		return nil
	}
	detail := hash + " merged; cleanup failed, tab " + tab + " left open"
	o.emit(Event{Kind: EvClosed, Ticket: id, Title: title, Detail: detail, Text: fmt.Sprintf(
		"  %s closed (%s); merged into %s, but CLEANUP_FAILED for %s / %s (git output is in %s); tab %s left open",
		id, commit, c.Base, wt, br, c.LogPath, tab)})
	return nil
}

// rebase rebases the ticket's branch onto Base, which moved on while the ticket ran, and returns the
// rebase as it went: conflicted when it stopped on conflicts, in progress still, or done when the
// ticket was set aside instead. It is called with repo held, and lets go of it only when done.
//
// git rebases what the worktree has checked out, which must be the branch, as its worker may have
// switched since finish looked, resolving a conflict. git won't rebase a worktree with uncommitted
// changes, as a check that rewrites the files it tracks leaves.
func (m *mergeRun) rebase() (r rebaseStop, conflicted, done bool) {
	o, c, keep, w := m.o, m.o.cfg, m.keep, m.w
	if on := o.offBranch(keep, w); on != "" {
		m.repo.unlock()
		o.leaveOffBranch(keep, w, on)
		return r, false, true
	}
	if files := o.uncommitted(keep, w.wt); len(files) > 0 {
		m.repo.unlock()
		o.leaveDirtyWorktree(keep, w, files)
		return r, false, true
	}
	r = rebaseStop{worker: w, onto: o.checkout.Head(keep, c.Repo, c.Base), head: o.checkout.Head(keep, c.Repo, w.br)}
	r.own = o.merger.CountCommits(keep, c.Repo, r.onto+".."+w.br)
	out, err := o.merger.Rebase(keep, w.wt, c.Base)
	o.log.Raw(out, err)
	if err != nil && !o.merger.RebaseInProgress(keep, w.wt) { // it didn't start, or stopped on nothing to resolve
		m.repo.unlock()
		o.leaveRebaseFailed(keep, w, err)
		return r, false, true
	}
	return r, err != nil, false
}

// resolveConflict hands r, a rebase stopped on conflicts, back to the ticket's worker to resolve
// when it can be, and checks the result, or sets the ticket aside. It is called with repo held.
func (m *mergeRun) resolveConflict(ctx context.Context, r rebaseStop) (mergeStep, *stopReason) {
	o, c, keep := m.o, m.o.cfg, m.keep
	r.files = o.merger.ConflictedFiles(keep, r.wt)
	if why := o.whyNotHandBack(keep, r, m.handedBack); why != "" {
		if err := o.abortRebase(keep, r.wt); err != nil {
			why += "; its rebase could not be aborted, so " + r.wt + " is left mid-rebase"
		}
		m.repo.unlock()
		o.leaveConflict(keep, r, "not handed back to its worker: "+why)
		return mergeDone, nil
	}
	if ctx.Err() != nil {
		return m.stoppedRebase(r)
	}
	m.repo.unlock()
	m.handedBack++
	m.fixes.overtaken(c.Base)
	// The worker takes minutes: let the other finished tickets merge meanwhile, and queue
	// behind them again to check its work and merge.
	m.queue.unlock()
	why, stop := o.handBack(ctx, r)
	m.queue.lock()
	if stop != nil {
		return mergeDone, stop // Ctrl+C or the usage limit: the rebase is left as it is, as the stop says
	}
	if why == "" && ctx.Err() != nil {
		return mergeDone, errInterrupted // the run stopped as the queue was taken again: the branch is left resolved
	}
	if why == "" {
		m.repo.lock()
		if m.dropUnchanged(r.onto, fmt.Sprintf("its worker found the changes of %s on %s already "+
			"and skipped them in the rebase", r.br, c.Base)) {
			return mergeDone, nil
		}
		m.repo.unlock()
		// Checked once, where it merges: a check on r.onto would be run again on the branch rebased.
		if o.baseMoved(keep, r) {
			o.info("  %s's worker finished the rebase, but %s moved on meanwhile: rebasing %s onto it before "+
				"checking it", r.id, c.Base, r.br)
			return mergeAgainAfterWorker, nil
		}
		o.info("  %s's worker finished the rebase; checking it with '%s'", r.id, c.Check)
		var res checked
		if res, stop = o.checkRebased(ctx, m.repo, m.queue, r, &m.fixes, true); stop != nil {
			return mergeDone, stop // Ctrl+C or the usage limit: a fix under way is left as it is
		}
		if why = o.failedResolved(r, res); why == "" {
			m.checked = res.at
		}
	}
	if why != "" {
		m.repo.lock()
		undone := o.undoResolution(keep, r)
		m.repo.unlock()
		o.appendNotes(keep, r.id, fmt.Sprintf("Orchestra: %s conflicted with %s in %s; "+
			"its worker was asked to resolve the rebase, but %s, so %s was set aside for review; %s.",
			r.br, c.Base, strings.Join(r.files, ", "), why, r.id, undone))
		o.leaveConflict(keep, r, fmt.Sprintf("handed back to its worker, but %s; %s", why, undone))
		return mergeDone, nil
	}
	return mergeAgainAfterWorker, nil // resolved and checked: merge, or rebase again if Base moved meanwhile
}

// stoppedRebase ends the merge as the run stops with r's rebase stopped on conflicts, before it is
// handed back to the ticket's worker, which could no longer be prompted: the rebase is aborted, so
// that the next run carries the ticket over as its worker left it. One that can't be aborted sets the
// ticket aside, its worktree left mid-rebase. It is called with repo held, and lets go of it.
func (m *mergeRun) stoppedRebase(r rebaseStop) (mergeStep, *stopReason) {
	err := m.o.abortRebase(m.keep, r.wt)
	m.repo.unlock()
	if err != nil {
		m.o.leaveConflict(m.keep, r, "not handed back to its worker, as the run stopped; its rebase could not be "+
			"aborted, so "+r.wt+" is left mid-rebase")
	}
	return mergeDone, errInterrupted
}

// checkClean checks the ticket's branch, rebased onto Base without conflicts as r says, or closes
// it as unchanged when git dropped its commits. It is called with repo held.
func (m *mergeRun) checkClean(ctx context.Context, r rebaseStop) (mergeStep, *stopReason) {
	o, c := m.o, m.o.cfg
	if m.dropUnchanged(c.Base, fmt.Sprintf("rebased onto %s, which has its changes already, %s changes nothing",
		c.Base, r.br)) { // git dropped its commits: Base has their changes
		return mergeDone, nil
	}
	m.repo.unlock()
	o.info("  rebased %s onto %s, which moved on while it ran", r.br, c.Base)
	if c.Check == "" {
		o.info("  no check command in .orchestra/settings.json: merging %s without checking the rebased code", r.br)
		return mergeAgain, nil
	}
	res, stop := o.checkRebased(ctx, m.repo, m.queue, r, &m.fixes, false)
	return m.afterCheck(r, res, stop)
}

// notFastForwarded is the reason to stop when w's branch, on top of Base, did not fast-forward onto
// it, git saying why in err. Most often uncommitted changes in the main checkout to files the branch
// changes are in the way: under .claude/, .beads/ or .orchestra/, which checkoutUnready leaves out
// (see Checkout.DirtyTree), and tracked files there change in tickets too. Those stop the run with
// DIRTY_TREE, naming the files. The caller holds repoMu.
func (o *Loop) notFastForwarded(ctx context.Context, w worker, err error) *stopReason {
	c := o.cfg
	if files := o.inTheWay(ctx, w.br); len(files) > 0 {
		return halt(ExitDirty, stopDirtyTree, ": uncommitted changes in %s to %s, which %s changes; stopping "+
			"before merging %s; worktree %s and tab %s left for review. Commit or undo them, then run again",
			c.Repo, strings.Join(files, ", "), w.br, w.br, w.wt, w.tab).
			causedBy(err).blocks("main checkout has uncommitted changes")
	}
	return halt(ExitMerge, stopMergeFailed, ": %s does not fast-forward onto %s%s; worktree %s and tab %s left for review",
		w.br, c.Base, because(err), w.wt, w.tab).causedBy(err).blocks("does not fast-forward onto " + c.Base)
}

// inTheWay lists, sorted, the files that br changes on top of Base and that the main checkout has
// uncommitted changes to, untracked files included: a fast-forward would overwrite them, so git
// refuses it. It lists none when git can't say.
func (o *Loop) inTheWay(ctx context.Context, br string) []string {
	c := o.cfg
	local := o.checkout.Changes(ctx, c.Repo)
	if len(local) == 0 {
		return nil
	}
	var files []string
	for _, f := range o.history.ChangedFiles(ctx, c.Repo, c.Base, br) {
		if _, ok := local[f]; ok {
			files = append(files, f)
		}
	}
	slices.Sort(files)
	return files
}

// uncommitted lists, sorted, the tracked files that worktree wt has uncommitted changes to, staged
// or not, outside .orchestra/run/: git won't rebase it then. Untracked files don't count, as they
// don't stop a rebase. It lists none when git can't say.
func (o *Loop) uncommitted(ctx context.Context, wt string) []string {
	var files []string
	for f, code := range o.checkout.Changes(ctx, wt) {
		if code != "??" && code != "!!" && !strings.HasPrefix(f, ".orchestra/run/") {
			files = append(files, f)
		}
	}
	slices.Sort(files)
	return files
}

// leaveDirtyWorktree sets aside w's ticket, not rebased onto Base, which moved on while it ran, as
// its worktree has uncommitted changes to files.
func (o *Loop) leaveDirtyWorktree(ctx context.Context, w worker, files []string) {
	c := o.cfg
	o.leaveFor(ctx, w.id, asideDirty, "uncommitted changes in its worktree", "", fmt.Sprintf(
		"%s closed, but %s has uncommitted changes to %s, so %s can't be rebased onto %s, "+
			"which moved on while it ran; worktree %s and tab %s left for review (commit or undo them, rebase onto %s, "+
			"check, merge)", w.id, w.wt, strings.Join(files, ", "), w.br, c.Base, w.wt, w.tab, c.Base))
}

// leaveRebaseFailed sets aside w's ticket, as git could not rebase its branch onto Base, saying
// why in err, without stopping on conflicts: no rebase is left in progress to resolve or abort.
func (o *Loop) leaveRebaseFailed(ctx context.Context, w worker, err error) {
	c := o.cfg
	o.leaveFor(ctx, w.id, asideRebaseFailed, "rebase failed", "", fmt.Sprintf(
		"%s closed, but git could not rebase %s onto %s, which moved on while it ran%s; "+
			"worktree %s and tab %s left for review (git output is in %s)",
		w.id, w.br, c.Base, because(err), w.wt, w.tab, c.LogPath))
}

// offBranch says what w's worktree has checked out when it isn't w's branch, as when its worker
// switched to another branch or detached HEAD, against its rules: "" when it is on the branch, or git
// can't say (its own errors then set the ticket aside if need be). A rebase in the worktree would
// rebase what it has checked out, so w.br would never come to be on top of Base.
func (o *Loop) offBranch(ctx context.Context, w worker) string {
	on, err := o.checkout.CurrentBranch(ctx, w.wt)
	switch {
	case err != nil || on == w.br:
		return ""
	case on != "":
		return on + " checked out"
	case o.merger.RebaseInProgress(ctx, w.wt):
		return "a rebase in progress, on a detached HEAD"
	}
	return "a detached HEAD"
}

// leaveOffBranch sets aside w's closed ticket, its worktree having on (see offBranch) rather than
// its branch: nothing is rebased, checked or merged.
func (o *Loop) leaveOffBranch(ctx context.Context, w worker, on string) {
	o.leaveFor(ctx, w.id, asideOffBranch, "worktree not on "+w.br, "", fmt.Sprintf(
		"%s closed, but its worktree %s has %s, not %s; worktree and tab %s left for review",
		w.id, w.wt, on, w.br, w.tab))
}

// failedResolved says why the branch of r's ticket, its rebase resolved by its worker, is set aside, as
// its check came out (res), or "" when the check passes.
func (o *Loop) failedResolved(r rebaseStop, res checked) string {
	if res.err == nil {
		return ""
	}
	why := fmt.Sprintf("%s on the resolved %s", o.failedHow(res.setup, res.err), r.br)
	if !errors.Is(res.err, errCheckTimedOut) {
		why += fmt.Sprintf(" (output is in %s)", res.output)
	}
	if h := res.handedBack(); h != "" {
		why += ", and was " + h
	}
	return why
}

// checksFailed sets aside the ticket of r, rebased onto r.onto or on top of it already, as its check
// failed there (res), and
// keeps it to be checked once more after Base moves on (see recheck), unless this was that once.
func (o *Loop) checksFailed(ctx context.Context, r rebaseStop, res checked) {
	c := o.cfg
	id := r.id
	why := checksFailedDetail(res.setup, errors.Is(res.err, errCheckTimedOut))
	tried := ""
	switch h := res.handedBack(); {
	case h != "":
		tried = fmt.Sprintf(", after it was handed back to its worker to fix %s (see its notes)", times(len(res.tries)))
		o.appendNotes(ctx, id, fmt.Sprintf("Orchestra: %s on %s (output is in %s); it was %s; "+
			"so %s was set aside for review.", o.failedHow(res.setup, res.err), r.branchOn(c.Base), res.output, h, id))
	case res.not != "":
		tried = "; not handed back to its worker: " + res.not
	}
	// Checked once more once Base moves on, unless this was that once (see recheck).
	again, later := " again", ""
	if !o.isRechecked(id) {
		again, later = "", o.awaitRecheck(ctx, r.worker, r.onto)
	}
	o.leaveFor(ctx, id, asideChecksFailed, why+again, "", fmt.Sprintf(
		"%s closed, but %s%s on %s%s; worktree %s and tab %s left for review (output is in %s)%s",
		id, o.failedHow(res.setup, res.err), again, r.branchOn(c.Base), tried, r.wt, r.tab, res.output, later))
}

// checksFailedDetail is the dashboard's short why for a ticket set aside as its check, or the setup
// before it, failed or did not finish.
func checksFailedDetail(setup, timedOut bool) string {
	switch {
	case setup && timedOut:
		return "setup timed out"
	case setup:
		return "setup failed"
	case timedOut:
		return "checks timed out"
	}
	return "checks failed"
}

// closedUnchanged cleans up after w's ticket, closed with no change of its own, as when tickets merged
// before it did what it was about: its branch has no commits beyond Base, or none that change anything,
// and its worktree is clean; why says which. Nothing is left to merge or to review, so its worktree,
// branch and tab are removed as after a merge, and the tickets it blocks needn't wait for it. repo,
// repoMu's guard, is not held by the caller: it is taken for the removal.
func (o *Loop) closedUnchanged(ctx context.Context, w worker, why string, repo *lockGuard) {
	c := o.cfg
	id, br, wt, tab := w.id, w.br, w.wt, w.tab
	o.clearUnmerged(ctx, id) // an unmerged label from an earlier run holds nothing now
	repo.lock()
	cleaned := o.removeWorktree(ctx, id, wt, br)
	repo.unlock()
	title := o.titleOf(id)
	if !cleaned {
		o.emit(Event{Kind: EvClosed, Ticket: id, Title: title, Detail: "no change; cleanup failed, tab " + tab + " left open",
			Text: fmt.Sprintf("  %s closed with no change to merge, but CLEANUP_FAILED for %s / %s (git output is in %s); "+
				"tab %s left open", id, wt, br, c.LogPath, tab)})
		return
	}
	o.emit(Event{Kind: EvClosed, Ticket: id, Title: title, Detail: "no change to merge", Text: fmt.Sprintf(
		"  %s closed with no change: %s, so there is nothing to merge; %s", id, why, o.closeWorkerTab(ctx, id, tab))})
}

// dropBranch moves the branch checked out in the clean worktree wt to Base, whose changes it has, so
// that git deletes it as merged; its commits change nothing, and git's reflog keeps them. A failure
// is logged: the branch is then left, as CLEANUP_FAILED says. The caller holds repoMu.
func (o *Loop) dropBranch(ctx context.Context, wt string) {
	out, err := o.merger.ResetBranch(ctx, wt, o.cfg.Base)
	o.log.Raw(out, err)
}

// removeWorktree removes ticket id's worktree wt and then its branch br, whose work is on Base, and
// reports whether both went; git's output goes in the log. What its worker's hooks noted is read
// first, for the run report. The caller holds repoMu.
func (o *Loop) removeWorktree(ctx context.Context, id, wt, br string) bool {
	o.readHooks(id, wt, true) // its worker is done: its notes are final
	out, err := o.worktrees.RemoveWorktree(ctx, o.cfg.Repo, wt)
	o.log.Raw(out, err)
	if err == nil {
		out, err = o.worktrees.DeleteBranch(ctx, o.cfg.Repo, br)
		o.log.Raw(out, err)
	}
	return err == nil
}

// closeWorkerTab closes tab, where ticket id's worker ran, once its work is merged (or it closed with
// nothing to merge) and its worktree and branch are removed, and says what was removed, for the line
// saying it closed (see closeOwnTab).
func (o *Loop) closeWorkerTab(ctx context.Context, id, tab string) string {
	if said, closed := o.closeOwnTab(ctx, id, tab); !closed {
		return "worktree and branch removed; " + said
	}
	return "worktree, branch and tab removed"
}

// closeOwnTab closes tab, where ticket id's worker ran, and reports whether it did, saying otherwise
// what became of the tab. It closes the tab only while Herdr has it labelled id, as the worker's tab
// was opened: Herdr numbers tabs afresh when it starts without restoring its last session, so a tab
// the last run left a worker in, or one recorded before Herdr restarted, may by now be another, the
// user's own included.
func (o *Loop) closeOwnTab(ctx context.Context, id, tab string) (string, bool) {
	label, open, err := o.tabs.TabLabel(ctx, tab)
	switch {
	case err != nil:
		o.log.Raw("", fmt.Errorf("cannot tell whether tab %s is still %s's, so it is left open: %w", tab, id, err))
		return "tab " + tab + " left open, as the terminal can't say whose it is", false
	case !open:
		return "its tab " + tab + " was closed already", false
	case label != id:
		return fmt.Sprintf("tab %s left open: the terminal has it labelled '%s' now, not %s, so it may not be its worker's",
			tab, label, id), false
	}
	if err := o.tabs.CloseTab(ctx, tab); err != nil {
		o.log.Raw("", fmt.Errorf("cannot close tab %s: %w", tab, err))
		return "tab " + tab + " could not be closed", false
	}
	return "tab " + tab + " closed", true
}

// leaveConflict sets aside a ticket whose branch conflicts with Base, saying why it was not
// resolved.
func (o *Loop) leaveConflict(ctx context.Context, r rebaseStop, why string) {
	c := o.cfg
	o.leaveFor(ctx, r.id, asideConflict, "conflicts with "+c.Base, conflictIn(r.files, c.Base), fmt.Sprintf(
		"%s closed, but %s conflicts with %s, which moved on while it ran (%s); "+
			"worktree %s and tab %s left for review (rebase onto %s, check, merge)",
		r.id, r.br, c.Base, why, r.wt, r.tab, c.Base))
}

// conflictIn says in a few words where a ticket's branch conflicts with base: in files, when git
// named them.
func conflictIn(files []string, base string) string {
	if len(files) == 0 {
		return "merge conflict with " + base
	}
	return "merge conflict in " + strings.Join(files, ", ")
}

// abortRebase gives up a rebase stopped in wt, logging git's output, and returns git's error: the
// worktree is then left mid-rebase.
func (o *Loop) abortRebase(ctx context.Context, wt string) error {
	out, err := o.merger.AbortRebase(ctx, wt)
	o.log.Raw(out, err)
	return err
}

// closedOutcome starts at one so an unset outcome is no outcome, never "merge".
type closedOutcome int

const (
	closedMerge closedOutcome = iota + 1
	closedNoCommit
	closedDirty
	closedNoChange // nothing to merge: no commits of its own and a clean worktree
)

// A closed ticket is merged only with a commit naming it and a clean worktree. One with no commit
// naming it, whose branch has no commits beyond Base (own, -1 when git can't count them) and whose
// worktree is clean, closed with no change of its own: there is nothing to merge, or to review.
func closedOutcomeOf(commit string, own int, worktreeDirty bool) closedOutcome {
	switch {
	case commit == "" && own == 0 && !worktreeDirty:
		return closedNoChange
	case commit == "":
		return closedNoCommit
	case worktreeDirty:
		return closedDirty
	}
	return closedMerge
}
