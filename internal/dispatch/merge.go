package dispatch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// finish merges w's closed ticket, or leaves it for review without a commit naming it or with
// uncommitted changes.
func (o *Loop) finish(ctx context.Context, w worker) *stopReason {
	c := o.cfg
	id, br, wt, tab := w.id, w.br, w.wt, w.tab
	keep := context.WithoutCancel(ctx) // a read cut short would look like no commit
	commit := o.merger.CommitNaming(keep, c.Repo, c.Base, br, id)
	switch closedOutcomeOf(commit, o.checkout.DirtyWorktree(keep, wt) != "") {
	case closedNoCommit:
		o.leaveUnmerged(keep, id, "CLOSED_WITHOUT_COMMIT")
		o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: "closed without a commit", Text: fmt.Sprintf(
			"  CLOSED_WITHOUT_COMMIT: no commit on %s names %s; worktree %s and tab %s left for review", br, id, wt, tab)})
	case closedDirty:
		o.leaveUnmerged(keep, id, "CLOSED_WITHOUT_COMMIT")
		o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: "closed with uncommitted changes", Text: fmt.Sprintf(
			"  CLOSED_WITHOUT_COMMIT: %s closed (%s) but %s has uncommitted changes; worktree and tab %s left for review",
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
// the merge queue meanwhile. A conflict left unresolved or a failing check leaves the ticket for
// review and the run goes on.
func (o *Loop) merge(ctx context.Context, w worker) *stopReason {
	c := o.cfg
	id, br, wt, tab := w.id, w.br, w.wt, w.tab
	// Once begun, rebasing, merging and the notes on it finish though Ctrl+C comes, so the
	// repository isn't left half merged; only the check and a worker resolving conflicts stop.
	keep := context.WithoutCancel(ctx)
	defer o.markFinishing(id, finishMerge)()
	repo, queue := &held{mu: &o.repoMu}, &held{mu: &o.mergeMu} // let go of even if the merge panics
	defer repo.release()
	queue.lock()
	defer queue.release() // held on every return; a hand-back lets go of it and takes it again
	handedBack := 0
	// Only merges move Base during a run, and they queue here; a second pass covers a commit made
	// by hand while the checks ran.
	for attempt := 0; attempt < 3; attempt++ {
		repo.lock()
		if o.merger.IsAncestor(keep, c.Repo, c.Base, br) {
			left := fmt.Sprintf(" before merging %s; worktree %s and tab %s left for review", br, wt, tab)
			if s := o.checkoutUnready(keep, left); s != nil {
				repo.unlock()
				o.leaveUnmerged(keep, id, string(s.kind)) // DIRTY_TREE or GIT_FAILED
				return s
			}
			commit := o.merger.CommitNaming(keep, c.Repo, c.Base, br, id)
			out, err := o.merger.FastForward(keep, c.Repo, br)
			o.log.Raw(out, err)
			if err != nil {
				repo.unlock()
				o.leaveUnmerged(keep, id, string(stopMergeFailed))
				return halt(ExitMerge, stopMergeFailed,
					": %s does not fast-forward onto %s; worktree %s and tab %s left for review", br, c.Base, wt, tab)
			}
			o.merged(keep, id)
			out, err = o.worktrees.RemoveWorktree(keep, c.Repo, wt)
			o.log.Raw(out, err)
			if err == nil {
				out, err = o.worktrees.DeleteBranch(keep, c.Repo, br)
				o.log.Raw(out, err)
			}
			repo.unlock()
			hash, _, _ := strings.Cut(commit, " ")
			title := o.titleOf(id)
			if err == nil {
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

		// Base moved on while the ticket ran: rebase it, still under the lock.
		r := rebaseStop{worker: w, onto: o.checkout.Head(keep, c.Repo, c.Base), head: o.checkout.Head(keep, c.Repo, br)}
		r.own = o.merger.CountCommits(keep, c.Repo, r.onto+".."+br)
		out, err := o.merger.Rebase(keep, wt, c.Base)
		o.log.Raw(out, err)
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
		repo.unlock()
		o.info("  rebased %s onto %s, which moved on while it ran", br, c.Base)
		if c.Check == "" {
			o.info("  no check command in .orchestra/settings.json: merging %s without checking the rebased code", br)
			continue
		}
		if output, err := o.runCheck(ctx, id, wt); err != nil {
			if ctx.Err() != nil {
				return errInterrupted
			}
			o.leaveUnmerged(keep, id, "CHECKS_FAILED")
			how, why := "fails", "checks failed"
			if errors.Is(err, errCheckTimedOut) {
				how, why = "did not finish within "+ShortDuration(o.checkTimeout()), "checks timed out"
			}
			o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: why, Text: fmt.Sprintf(
				"  CHECKS_FAILED: %s closed, but '%s' %s on %s rebased onto %s; "+
					"worktree %s and tab %s left for review (output is in %s)",
				id, c.Check, how, br, c.Base, wt, tab, output)})
			return nil
		}
		o.info("  '%s' passes on the rebased %s", c.Check, br)
		// Lock again and merge; if Base moved once more meanwhile, rebase and check again.
	}
	o.leaveUnmerged(keep, id, "MERGE_CONFLICT")
	o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: c.Base + " kept changing", Text: fmt.Sprintf(
		"  MERGE_CONFLICT: %s closed, but %s kept changing while its checks ran (commits made by hand?); "+
			"worktree %s and tab %s left for review", id, c.Base, wt, tab)})
	return nil
}

// closeWorkerTab closes tab, where ticket id's worker ran, once its work is merged and its worktree
// and branch are removed, and says what was removed, for the line saying it merged. It closes the
// tab only while Herdr has it labelled id, as the worker's tab was opened: Herdr numbers tabs afresh
// when it starts without restoring its last session, so a tab the last run left a worker in, or one
// recorded before Herdr restarted, may by now be another, the user's own included.
func (o *Loop) closeWorkerTab(ctx context.Context, id, tab string) string {
	const removed = "worktree and branch removed; "
	label, open, err := o.tabs.TabLabel(ctx, tab)
	switch {
	case err != nil:
		o.log.Raw("", fmt.Errorf("cannot tell whether tab %s is still %s's, so it is left open: %w", tab, id, err))
		return removed + "tab " + tab + " left open, as Herdr can't say whose it is"
	case !open:
		return removed + "its tab " + tab + " was closed already"
	case label != id:
		return fmt.Sprintf("%stab %s left open: Herdr has it labelled '%s' now, not %s, so it may not be its worker's",
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
	o.emit(Event{Kind: EvWarn, Ticket: r.id, Aside: true, Detail: "conflicts with " + c.Base, Text: fmt.Sprintf(
		"  MERGE_CONFLICT: %s closed, but %s conflicts with %s, which moved on while it ran (%s); "+
			"worktree %s and tab %s left for review (rebase onto %s, check, merge)",
		r.id, r.br, c.Base, why, r.wt, r.tab, c.Base)})
}

// abortRebase gives up a rebase stopped in wt, logging git's output, and returns git's error: the
// worktree is then left mid-rebase.
func (o *Loop) abortRebase(ctx context.Context, wt string) error {
	out, err := o.merger.AbortRebase(ctx, wt)
	o.log.Raw(out, err)
	return err
}

// errCheckTimedOut is runCheck's error for a check stopped at its time limit.
var errCheckTimedOut = errors.New("check timed out")

// checkTimeout is how long the check command may run.
func (o *Loop) checkTimeout() time.Duration {
	return orDefault(o.cfg.CheckTimeout, project.DefaultCheckTimeout)
}

// runCheck runs the project's check command in ticket id's worktree wt. It gives up after
// checkTimeout, stopping everything the check started, and returns errCheckTimedOut then. When the
// check fails, the end of its output goes in the log and the whole of it in wt's checkLogName (see
// saveCheckOutput); runCheck returns where the output is. When it passes, each FLAKY: line it printed
// is a warning (see reportFlaky).
func (o *Loop) runCheck(ctx context.Context, id, wt string) (string, error) {
	limit := o.checkTimeout()
	checkCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, err := command.GroupOutput(checkCtx, 5*time.Second, wt, "sh", "-c", o.cfg.Check)
	if err != nil && ctx.Err() == nil && errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("%w: stopped after %s (%v)", errCheckTimedOut, ShortDuration(limit), err)
	}
	if err != nil {
		o.log.Raw(lastLines(string(out), 40), fmt.Errorf("check '%s' in %s: %w", o.cfg.Check, wt, err))
		return o.saveCheckOutput(wt, out), err
	}
	o.reportFlaky(id, out)
	return "", nil
}

// checkLogName is the file in a worktree's .orchestra/run/ that holds the whole output of the last
// check that failed there: the log keeps only its end, where a long failure message can push out
// the name of the test that failed.
const checkLogName = "check.log"

// saveCheckOutput writes a failed check's whole output to checkLogName in the worktree wt, left for
// review with it, and returns the file's path. The worker can change .orchestra/run/, so the file
// is reached through an os.Root, as every run file is (see project.OpenRun). A file that can't be
// written is logged, and the log's path returned: it has the end of the output.
func (o *Loop) saveCheckOutput(wt string, out []byte) string {
	rel := project.RunPath(checkLogName)
	root, err := project.OpenRun(wt)
	if err == nil {
		err = project.WriteRun(root, wt, rel, out, 0o644)
		_ = root.Close() // nothing written is lost: WriteRun closed its file
	}
	if err != nil {
		o.log.Raw("", fmt.Errorf("cannot keep the check's whole output in %s: %w", wt, err))
		return o.cfg.LogPath
	}
	return filepath.Join(wt, rel)
}

// flakyPrefix starts a line of a check's output that says a test failed and then passed on a rerun:
// a convention for any project's check, which the README documents.
const flakyPrefix = "FLAKY:"

// reportFlaky warns, naming ticket id, of each FLAKY: line in the output of its passing check: the
// check passed, so the ticket merges, but a test that passes only on a rerun is a bug to fix, and
// nobody would see it otherwise.
func (o *Loop) reportFlaky(id string, out []byte) {
	for line := range strings.Lines(string(out)) {
		test, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), flakyPrefix)
		if !ok {
			continue
		}
		if test = strings.TrimSpace(test); test == "" {
			test = "it doesn't say which"
		}
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  FLAKY: %s's check '%s' passed, but a test failed and then passed on a rerun: %s", id, o.cfg.Check, test)})
	}
}

// refreshBranch rebases a returning ticket's branch onto Base, which has moved on since the branch
// was cut, so its worker starts from current code. A failed rebase is undone and it returns false:
// the branch conflicts with Base.
func (o *Loop) refreshBranch(ctx context.Context, wt, br string) bool {
	c := o.cfg
	if o.merger.IsAncestor(ctx, c.Repo, c.Base, br) {
		return true // already on top of Base
	}
	if d := o.checkout.DirtyWorktree(ctx, wt); d != "" {
		o.emit(Event{Kind: EvWarn, Text: fmt.Sprintf(
			"  REBASE_SKIPPED: %s has uncommitted changes, so %s stays behind %s until it merges", wt, br, c.Base)})
		return true
	}
	out, err := o.merger.Rebase(ctx, wt, c.Base)
	o.log.Raw(out, err)
	if err != nil {
		if err := o.abortRebase(ctx, wt); err != nil {
			o.emit(Event{Kind: EvWarn, Text: fmt.Sprintf("  REBASE_ABORT_FAILED: %s is left mid-rebase onto %s; "+
				"resolve and continue it there, or run git rebase --abort", wt, c.Base)})
		}
		return false
	}
	o.info("  rebased %s onto %s", br, c.Base)
	return true
}
