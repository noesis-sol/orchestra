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
		o.closedUnchanged(keep, w, fmt.Sprintf("%s has no commits beyond %s and its worktree is clean", br, c.Base))
	case closedNoCommit:
		o.leaveUnmerged(keep, id, "CLOSED_WITHOUT_COMMIT")
		o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: "closed without a commit", Text: fmt.Sprintf(
			"  CLOSED_WITHOUT_COMMIT: no commit on %s names %s; worktree %s and tab %s left for review", br, id, wt, tab)})
	case closedDirty:
		o.leaveUnmerged(keep, id, "CLOSED_UNCOMMITTED")
		o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: "closed with uncommitted changes", Text: fmt.Sprintf(
			"  CLOSED_UNCOMMITTED: %s closed (%s) but %s has uncommitted changes; worktree and tab %s left for review",
			id, commit, wt, tab)})
	case closedMerge:
		return o.merge(ctx, w)
	}
	return nil
}

// merge brings w's finished ticket's branch onto Base, one ticket at a time. When other tickets
// merged while it ran, the branch is rebased first and, since the rebased code is untested, the
// project's check command runs again before it merges. A rebase that stops on conflicts is handed
// back to the ticket's worker to resolve, when it can be (see whyNotHandBack), without holding up
// the merge queue meanwhile, and so is a check that fails on the rebased branch, to fix (see
// checkRebased). A conflict left unresolved or a failing check leaves the ticket for review and the
// run goes on; a failing check is tried once more after Base moves on (see recheck).
// A branch whose commits, taken together, change nothing, before its rebase or after (by git, or by
// its worker skipping those Base has already), has nothing to merge and is cleaned up as such (see
// closedUnchanged).
func (o *Loop) merge(ctx context.Context, w worker) *stopReason {
	c := o.cfg
	id, br, wt, tab := w.id, w.br, w.wt, w.tab
	// Once begun, rebasing, merging and the notes on it finish though Ctrl+C comes, so the
	// repository isn't left half merged; only the check and a worker resolving conflicts stop.
	keep := context.WithoutCancel(ctx)
	defer o.markFinishing(id, finishMerge)()
	repo, queue := &held{mu: &o.repoMu}, &held{mu: mergeTurn{o: o, id: id}} // let go of even if the merge panics
	defer repo.release()
	queue.lock()
	defer queue.release() // held on every return; a hand-back lets go of it and takes it again
	// The hand-backs to its worker so far: to resolve conflicts, and to fix a failed check.
	handedBack, fixes := 0, 0
	// Only merges move Base during a run, and they queue here; a second pass covers a commit made
	// by hand while the checks ran.
	for attempt := 0; attempt < 3; attempt++ {
		repo.lock()
		// Its commits may undo each other, as when its worker found the change on Base already and
		// reverted its own: rebasing them would replay the revert onto Base's change.
		if o.merger.Unchanged(keep, c.Repo, c.Base, br) {
			o.dropBranch(keep, wt)
			repo.unlock()
			o.closedUnchanged(keep, w, fmt.Sprintf("the commits on %s, taken together, change nothing", br))
			return nil
		}
		if o.merger.IsAncestor(keep, c.Repo, c.Base, br) {
			left := fmt.Sprintf(" before merging %s; worktree %s and tab %s left for review", br, wt, tab)
			if s := o.checkoutUnready(keep, left, heldAlready{}); s != nil {
				repo.unlock()
				o.leaveUnmerged(keep, id, string(s.kind)) // DIRTY_TREE or GIT_FAILED
				return s
			}
			commit := o.merger.CommitNaming(keep, c.Repo, c.Base, br, id)
			out, err := o.merger.FastForward(keep, c.Repo, br)
			o.log.Raw(out, err)
			if err != nil {
				s := o.notFastForwarded(keep, w, err)
				repo.unlock()
				o.leaveUnmerged(keep, id, string(s.kind)) // DIRTY_TREE or MERGE_FAILED
				return s
			}
			o.merged(keep, id)
			o.countMerge()
			cleaned := o.removeWorktree(keep, id, wt, br)
			repo.unlock()
			hash, _, _ := strings.Cut(commit, " ")
			title := o.titleOf(id)
			if cleaned {
				removed := o.closeWorkerTab(keep, id, tab)
				o.emit(Event{Kind: EvClosed, Ticket: id, Title: title, Detail: hash + " merged into " + c.Base, Text: fmt.Sprintf(
					"  %s closed (%s); merged into %s, %s", id, commit, c.Base, removed)})
			} else {
				detail := hash + " merged; cleanup failed, tab " + tab + " left open"
				o.emit(Event{Kind: EvClosed, Ticket: id, Title: title, Detail: detail, Text: fmt.Sprintf(
					"  %s closed (%s); merged into %s, but CLEANUP_FAILED for %s / %s (git output is in %s); tab %s left open",
					id, commit, c.Base, wt, br, c.LogPath, tab)})
			}
			return nil
		}

		// Base moved on while the ticket ran: rebase it, still under the lock. git rebases what the
		// worktree has checked out, which must be br, as its worker may have switched since finish
		// looked, resolving a conflict. git won't rebase a worktree with uncommitted changes, as a
		// check that rewrites the files it tracks leaves.
		if on := o.offBranch(keep, w); on != "" {
			repo.unlock()
			o.leaveOffBranch(keep, w, on)
			return nil
		}
		if files := o.uncommitted(keep, wt); len(files) > 0 {
			repo.unlock()
			o.leaveDirtyWorktree(keep, w, files)
			return nil
		}
		r := rebaseStop{worker: w, onto: o.checkout.Head(keep, c.Repo, c.Base), head: o.checkout.Head(keep, c.Repo, br)}
		r.own = o.merger.CountCommits(keep, c.Repo, r.onto+".."+br)
		out, err := o.merger.Rebase(keep, wt, c.Base)
		o.log.Raw(out, err)
		if err != nil && !o.merger.RebaseInProgress(keep, wt) { // it didn't start, or stopped on nothing to resolve
			repo.unlock()
			o.leaveRebaseFailed(keep, w, err)
			return nil
		}
		if err != nil {
			r.files = o.merger.ConflictedFiles(keep, wt)
			if why := o.whyNotHandBack(keep, r, handedBack); why != "" {
				if err := o.abortRebase(keep, wt); err != nil {
					why += "; its rebase could not be aborted, so " + wt + " is left mid-rebase"
				}
				repo.unlock()
				o.leaveConflict(keep, r, "not handed back to its worker: "+why)
				return nil
			}
			repo.unlock()
			handedBack++
			// The worker takes minutes: let the other finished tickets merge meanwhile, and queue
			// behind them again to check its work and merge.
			queue.unlock()
			why, stop := o.handBack(ctx, r)
			queue.lock()
			if stop != nil {
				return stop // Ctrl+C: the rebase is left as it is, as the INTERRUPTED line says
			}
			if why == "" {
				repo.lock()
				if o.merger.Unchanged(keep, c.Repo, r.onto, br) { // its worker skipped what Base has already
					o.dropBranch(keep, wt)
					repo.unlock()
					o.closedUnchanged(keep, w, fmt.Sprintf("its worker found the changes of %s on %s already "+
						"and skipped them in the rebase", br, c.Base))
					return nil
				}
				repo.unlock()
				o.info("  %s's worker finished the rebase; checking it with '%s'", id, c.Check)
				var res checked
				if res, stop = o.checkRebased(ctx, repo, queue, r, &fixes, true); stop != nil {
					return stop // Ctrl+C: a fix under way is left as it is
				}
				why = o.failedResolved(r, res)
			}
			if why != "" {
				repo.lock()
				undone := o.undoResolution(keep, r)
				repo.unlock()
				o.appendNotes(keep, id, fmt.Sprintf("Orchestra: %s conflicted with %s in %s; "+
					"its worker was asked to resolve the rebase, but %s, so %s was set aside for review; %s.",
					br, c.Base, strings.Join(r.files, ", "), why, id, undone))
				o.leaveConflict(keep, r, fmt.Sprintf("handed back to its worker, but %s; %s", why, undone))
				return nil
			}
			attempt-- // resolved and checked: merge, or rebase again if Base moved meanwhile
			continue
		}
		if o.merger.Unchanged(keep, c.Repo, c.Base, br) { // git dropped its commits: Base has their changes
			o.dropBranch(keep, wt)
			repo.unlock()
			o.closedUnchanged(keep, w, fmt.Sprintf("rebased onto %s, which has its changes already, %s changes nothing",
				c.Base, br))
			return nil
		}
		repo.unlock()
		o.info("  rebased %s onto %s, which moved on while it ran", br, c.Base)
		if c.Check == "" {
			o.info("  no check command in .orchestra/settings.json: merging %s without checking the rebased code", br)
			continue
		}
		res, stop := o.checkRebased(ctx, repo, queue, r, &fixes, false)
		if stop != nil {
			return stop
		}
		if res.err != nil {
			o.checksFailed(keep, r, res)
			return nil
		}
		if len(res.tries) > 0 {
			attempt-- // fixed: the queue was let go of while its worker worked, so Base may have moved
		}
		// Lock again and merge; if Base moved once more meanwhile, rebase and check again.
	}
	o.leaveUnmerged(keep, id, "MERGE_CONFLICT")
	o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: c.Base + " kept changing",
		Blocked: c.Base + " kept changing", Text: fmt.Sprintf(
			"  MERGE_CONFLICT: %s closed, but %s kept changing while its checks ran (commits made by hand?); "+
				"worktree %s and tab %s left for review", id, c.Base, wt, tab)})
	return nil
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
	o.leaveUnmerged(ctx, w.id, "DIRTY_WORKTREE")
	o.emit(Event{Kind: EvWarn, Ticket: w.id, Aside: true, Detail: "uncommitted changes in its worktree", Text: fmt.Sprintf(
		"  DIRTY_WORKTREE: %s closed, but %s has uncommitted changes to %s, so %s can't be rebased onto %s, "+
			"which moved on while it ran; worktree %s and tab %s left for review (commit or undo them, rebase onto %s, "+
			"check, merge)", w.id, w.wt, strings.Join(files, ", "), w.br, c.Base, w.wt, w.tab, c.Base)})
}

// leaveRebaseFailed sets aside w's ticket, as git could not rebase its branch onto Base, saying
// why in err, without stopping on conflicts: no rebase is left in progress to resolve or abort.
func (o *Loop) leaveRebaseFailed(ctx context.Context, w worker, err error) {
	c := o.cfg
	o.leaveUnmerged(ctx, w.id, "REBASE_FAILED")
	o.emit(Event{Kind: EvWarn, Ticket: w.id, Aside: true, Detail: "rebase failed", Text: fmt.Sprintf(
		"  REBASE_FAILED: %s closed, but git could not rebase %s onto %s, which moved on while it ran%s; "+
			"worktree %s and tab %s left for review (git output is in %s)",
		w.id, w.br, c.Base, because(err), w.wt, w.tab, c.LogPath)})
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
	o.leaveUnmerged(ctx, w.id, "OFF_BRANCH")
	o.emit(Event{Kind: EvWarn, Ticket: w.id, Aside: true, Detail: "worktree not on " + w.br, Text: fmt.Sprintf(
		"  OFF_BRANCH: %s closed, but its worktree %s has %s, not %s; worktree and tab %s left for review",
		w.id, w.wt, on, w.br, w.tab)})
}

// failedResolved says why the branch of r's ticket, its rebase resolved by its worker, is set aside, as
// its check came out (res), or "" when the check passes.
func (o *Loop) failedResolved(r rebaseStop, res checked) string {
	if res.err == nil {
		return ""
	}
	why := fmt.Sprintf("%s %s on the resolved %s", o.failedWhat(res.setup), o.checkHow(res.err), r.br)
	if !errors.Is(res.err, errCheckTimedOut) {
		why += fmt.Sprintf(" (output is in %s)", res.output)
	}
	if h := res.handedBack(); h != "" {
		why += ", and was " + h
	}
	return why
}

// checksFailed sets aside the ticket of r, rebased onto r.onto, as its check failed there (res), and
// keeps it to be checked once more after Base moves on (see recheck), unless this was that once.
func (o *Loop) checksFailed(ctx context.Context, r rebaseStop, res checked) {
	c := o.cfg
	id := r.id
	o.leaveUnmerged(ctx, id, "CHECKS_FAILED")
	why := "checks failed"
	if res.setup {
		why = "setup failed"
	}
	if errors.Is(res.err, errCheckTimedOut) {
		why = strings.Replace(why, "failed", "timed out", 1)
	}
	tried := ""
	switch h := res.handedBack(); {
	case h != "":
		tried = fmt.Sprintf(", after it was handed back to its worker to fix %s (see its notes)", times(len(res.tries)))
		o.appendNotes(ctx, id, fmt.Sprintf("Orchestra: %s %s on %s rebased onto %s (output is in %s); it was %s; "+
			"so %s was set aside for review.", o.failedWhat(res.setup), o.checkHow(res.err), r.br, c.Base, res.output, h, id))
	case res.not != "":
		tried = "; not handed back to its worker: " + res.not
	}
	// Checked once more once Base moves on, unless this was that once (see recheck).
	again, later := " again", ""
	if !o.isRechecked(id) {
		again, later = "", o.awaitRecheck(ctx, r.worker, r.onto)
	}
	o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: why + again, Text: fmt.Sprintf(
		"  CHECKS_FAILED: %s closed, but %s %s%s on %s rebased onto %s%s; "+
			"worktree %s and tab %s left for review (output is in %s)%s",
		id, o.failedWhat(res.setup), o.checkHow(res.err), again, r.br, c.Base, tried, r.wt, r.tab, res.output, later)})
}

// closedUnchanged cleans up after w's ticket, closed with no change of its own, as when tickets merged
// before it did what it was about: its branch has no commits beyond Base, or none that change anything,
// and its worktree is clean; why says which. Nothing is left to merge or to review, so its worktree,
// branch and tab are removed as after a merge, and the tickets it blocks needn't wait for it.
func (o *Loop) closedUnchanged(ctx context.Context, w worker, why string) {
	c := o.cfg
	id, br, wt, tab := w.id, w.br, w.wt, w.tab
	o.merged(ctx, id) // an unmerged label from an earlier run holds nothing now
	o.repoMu.Lock()
	cleaned := o.removeWorktree(ctx, id, wt, br)
	o.repoMu.Unlock()
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
// saying it closed. It closes the tab only while Herdr has it labelled id, as the worker's tab was
// opened: Herdr numbers tabs afresh when it starts without restoring its last session, so a tab the
// last run left a worker in, or one recorded before Herdr restarted, may by now be another, the
// user's own included.
func (o *Loop) closeWorkerTab(ctx context.Context, id, tab string) string {
	const removed = "worktree and branch removed; "
	label, open, err := o.tabs.TabLabel(ctx, tab)
	switch {
	case err != nil:
		o.log.Raw("", fmt.Errorf("cannot tell whether tab %s is still %s's, so it is left open: %w", tab, id, err))
		return removed + "tab " + tab + " left open, as the terminal can't say whose it is"
	case !open:
		return removed + "its tab " + tab + " was closed already"
	case label != id:
		return fmt.Sprintf("%stab %s left open: the terminal has it labelled '%s' now, not %s, so it may not be its worker's",
			removed, tab, label, id)
	}
	if err := o.tabs.CloseTab(ctx, tab); err != nil {
		o.log.Raw("", fmt.Errorf("cannot close tab %s: %w", tab, err))
		return removed + "tab " + tab + " could not be closed"
	}
	return "worktree, branch and tab removed"
}

// leaveConflict sets aside a ticket whose branch conflicts with Base, saying why it was not
// resolved.
func (o *Loop) leaveConflict(ctx context.Context, r rebaseStop, why string) {
	c := o.cfg
	o.leaveUnmerged(ctx, r.id, "MERGE_CONFLICT")
	o.emit(Event{Kind: EvWarn, Ticket: r.id, Aside: true, Detail: "conflicts with " + c.Base,
		Blocked: conflictIn(r.files, c.Base), Text: fmt.Sprintf(
			"  MERGE_CONFLICT: %s closed, but %s conflicts with %s, which moved on while it ran (%s); "+
				"worktree %s and tab %s left for review (rebase onto %s, check, merge)",
			r.id, r.br, c.Base, why, r.wt, r.tab, c.Base)})
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
