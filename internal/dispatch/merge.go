package dispatch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// finish merges a closed ticket, or leaves it for review without a commit naming it or with
// uncommitted changes.
func (o *Loop) finish(ctx context.Context, id, br, wt, tab string) *stopReason {
	c := o.cfg
	commit := o.merger.CommitNaming(c.Repo, c.Base, br, id)
	switch closedOutcomeOf(commit, o.checkout.DirtyWorktree(wt) != "") {
	case closedNoCommit:
		o.leaveUnmerged(id, "CLOSED_WITHOUT_COMMIT")
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  CLOSED_WITHOUT_COMMIT: no commit on %s names %s; worktree %s and tab %s left for review", br, id, wt, tab)})
	case closedDirty:
		o.leaveUnmerged(id, "CLOSED_WITHOUT_COMMIT")
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  CLOSED_WITHOUT_COMMIT: %s closed (%s) but %s has uncommitted changes; worktree and tab %s left for review", id, commit, wt, tab)})
	case closedMerge:
		return o.merge(ctx, id, br, wt, tab)
	}
	return nil
}

// merge brings a finished ticket's branch onto Base, one ticket at a time. When other tickets
// merged while it ran, the branch is rebased first and, since the rebased code is untested, the
// project's check command runs again before it merges. A conflict or a failing check leaves the
// ticket for review and the run goes on.
func (o *Loop) merge(ctx context.Context, id, br, wt, tab string) *stopReason {
	c := o.cfg
	o.mergeMu.Lock()
	defer o.mergeMu.Unlock()
	// Only merges move Base during a run, and they queue here; a second pass covers a commit made
	// by hand while the checks ran.
	for attempt := 0; attempt < 3; attempt++ {
		o.repoMu.Lock()
		if o.merger.IsAncestor(c.Repo, c.Base, br) {
			if s := o.checkoutUnready(fmt.Sprintf(" before merging %s; worktree %s and tab %s left for review", br, wt, tab)); s != nil {
				o.repoMu.Unlock()
				o.leaveUnmerged(id, "DIRTY_TREE")
				return s
			}
			commit := o.merger.CommitNaming(c.Repo, c.Base, br, id)
			out, err := o.merger.FastForward(c.Repo, br)
			o.log.Raw(out, err)
			if err != nil {
				o.repoMu.Unlock()
				o.leaveUnmerged(id, "MERGE_FAILED")
				return halt(ExitMerge, "MERGE_FAILED: %s does not fast-forward onto %s; worktree %s and tab %s left for review", br, c.Base, wt, tab)
			}
			o.merged(id)
			out, err = o.worktrees.RemoveWorktree(c.Repo, wt)
			o.log.Raw(out, err)
			if err == nil {
				out, err = o.worktrees.DeleteBranch(c.Repo, br)
				o.log.Raw(out, err)
			}
			o.repoMu.Unlock()
			hash, _, _ := strings.Cut(commit, " ")
			if err == nil {
				o.tabs.CloseTab(tab)
				o.emit(Event{Kind: EvClosed, Ticket: id, Detail: hash + " merged into " + c.Base, Text: fmt.Sprintf(
					"  %s closed (%s); merged into %s, worktree, branch and tab removed", id, commit, c.Base)})
			} else {
				o.emit(Event{Kind: EvClosed, Ticket: id, Detail: hash + " merged; cleanup failed, tab " + tab + " left open", Text: fmt.Sprintf(
					"  %s closed (%s); merged into %s, but CLEANUP_FAILED for %s / %s (git output is in %s); tab %s left open", id, commit, c.Base, wt, br, c.LogPath, tab)})
			}
			return nil
		}

		// Base moved on while the ticket ran: rebase it, still under the lock.
		out, err := o.merger.Rebase(wt, c.Base)
		o.log.Raw(out, err)
		if err != nil {
			o.merger.AbortRebase(wt)
			o.repoMu.Unlock()
			o.leaveUnmerged(id, "MERGE_CONFLICT")
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  MERGE_CONFLICT: %s closed, but %s conflicts with %s, which moved on while it ran; worktree %s and tab %s left for review (rebase onto %s, check, merge)",
				id, br, c.Base, wt, tab, c.Base)})
			return nil
		}
		o.repoMu.Unlock()
		o.info("  rebased %s onto %s, which moved on while it ran", br, c.Base)
		if c.Check == "" {
			o.info("  no check command in .orchestra/settings.json: merging %s without checking the rebased code", br)
			continue
		}
		if err := o.runCheck(ctx, wt); err != nil {
			if ctx.Err() != nil {
				return errInterrupted
			}
			o.leaveUnmerged(id, "CHECKS_FAILED")
			how := "fails"
			if errors.Is(err, errCheckTimedOut) {
				how = "did not finish within " + ShortDuration(o.checkTimeout())
			}
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  CHECKS_FAILED: %s closed, but '%s' %s on %s rebased onto %s; worktree %s and tab %s left for review (output is in %s)",
				id, c.Check, how, br, c.Base, wt, tab, c.LogPath)})
			return nil
		}
		o.info("  '%s' passes on the rebased %s", c.Check, br)
		// Lock again and merge; if Base moved once more meanwhile, rebase and check again.
	}
	o.leaveUnmerged(id, "MERGE_CONFLICT")
	o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
		"  MERGE_CONFLICT: %s closed, but %s kept changing while its checks ran (commits made by hand?); worktree %s and tab %s left for review", id, c.Base, wt, tab)})
	return nil
}

// errCheckTimedOut is runCheck's error for a check stopped at its time limit.
var errCheckTimedOut = errors.New("check timed out")

// checkTimeout is how long the check command may run.
func (o *Loop) checkTimeout() time.Duration {
	return orDefault(o.cfg.CheckTimeout, project.DefaultCheckTimeout)
}

// runCheck runs the project's check command in the worktree, logging the end of its output if it
// fails. It gives up after checkTimeout, stopping everything the check started, and returns
// errCheckTimedOut then.
func (o *Loop) runCheck(ctx context.Context, wt string) error {
	limit := o.checkTimeout()
	checkCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, err := command.GroupOutput(checkCtx, 5*time.Second, wt, "sh", "-c", o.cfg.Check)
	if err != nil && ctx.Err() == nil && errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("%w: stopped after %s (%v)", errCheckTimedOut, ShortDuration(limit), err)
	}
	if err != nil {
		o.log.Raw(lastLines(string(out), 40), fmt.Errorf("check '%s' in %s: %w", o.cfg.Check, wt, err))
	}
	return err
}

// refreshBranch rebases a returning ticket's branch onto Base, which has moved on since the branch
// was cut, so its worker starts from current code. A failed rebase is undone and it returns false:
// the branch conflicts with Base.
func (o *Loop) refreshBranch(wt, br string) bool {
	c := o.cfg
	if o.merger.IsAncestor(c.Repo, c.Base, br) {
		return true // already on top of Base
	}
	if d := o.checkout.DirtyWorktree(wt); d != "" {
		o.emit(Event{Kind: EvWarn, Text: fmt.Sprintf("  REBASE_SKIPPED: %s has uncommitted changes, so %s stays behind %s until it merges", wt, br, c.Base)})
		return true
	}
	out, err := o.merger.Rebase(wt, c.Base)
	o.log.Raw(out, err)
	if err != nil {
		o.merger.AbortRebase(wt)
		return false
	}
	o.info("  rebased %s onto %s", br, c.Base)
	return true
}
