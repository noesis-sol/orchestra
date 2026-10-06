package dispatch

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// finish merges w's closed ticket, or leaves it for review without a commit naming it or with
// uncommitted changes. One with no commits of its own and a clean worktree has nothing to merge, and
// is cleaned up as after a merge (see closedUnchanged).
func (o *Loop) finish(ctx context.Context, w worker) *stopReason {
	c := o.cfg
	id, br, wt, tab := w.id, w.br, w.wt, w.tab
	keep := context.WithoutCancel(ctx) // a read cut short would look like no commit
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
	repo, queue := &held{mu: &o.repoMu}, &held{mu: &o.mergeMu} // let go of even if the merge panics
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
			if s := o.checkoutUnready(keep, left); s != nil {
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

// errCheckTimedOut is runCheck's error for a check stopped at its time limit.
var errCheckTimedOut = errors.New("check timed out")

// checkTimeout is how long the check command may run.
func (o *Loop) checkTimeout() time.Duration {
	return orDefault(o.cfg.CheckTimeout, project.DefaultCheckTimeout)
}

// runCheck runs the project's check command in w's worktree, its branch rebased onto the commit
// onto. It gives up after checkTimeout, stopping everything the check started, and returns
// errCheckTimedOut then. When the check fails, the end of its output goes in the log and the whole of
// it in the worktree's checkLogName (see saveCheckOutput), and what it said goes to the reviewer (see
// noteCheckFailed); runCheck returns where the output is. When it passes, each FLAKY: line it printed
// is a warning (see reportFlaky).
func (o *Loop) runCheck(ctx context.Context, w worker, onto string) (string, error) {
	out, output, err := o.runIn(ctx, w, o.cfg.Check, checkLogName)
	if err != nil {
		if ctx.Err() == nil { // stopped by Ctrl+C, it says nothing of the ticket
			o.noteCheckFailed(ctx, w, onto, output, out, err, "")
		}
		return output, err
	}
	o.reportFlaky(w.id, "", out)
	return "", nil
}

// runIn runs the command line cmd, the check or the setup, in w's worktree and returns its output. It
// gives up after checkTimeout, stopping everything cmd started, and returns errCheckTimedOut then.
// When cmd fails, the end of its output goes in the log and the whole of it in the worktree's file
// logName (see saveCheckOutput), whose path it returns as output.
func (o *Loop) runIn(ctx context.Context, w worker, cmd, logName string) (out []byte, output string, err error) {
	limit := o.checkTimeout()
	runCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, err = command.GroupOutput(runCtx, 5*time.Second, w.wt, "sh", "-c", cmd)
	if err != nil && ctx.Err() == nil && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("%w: stopped after %s (%v)", errCheckTimedOut, command.ShortDuration(limit), err)
	}
	if err != nil {
		o.log.Raw(lastLines(string(out), 40), fmt.Errorf("'%s' in %s: %w", cmd, w.wt, err))
		return out, o.saveCheckOutput(w.wt, logName, out), err
	}
	return out, "", nil
}

// checkHow says how the check failed with err, for a line about it: it fails, or did not finish.
func (o *Loop) checkHow(err error) string {
	if errors.Is(err, errCheckTimedOut) {
		return "did not finish within " + command.ShortDuration(o.checkTimeout())
	}
	return "fails"
}

// checkLogName is the file in a worktree's .orchestra/run/ that holds the whole output of the last
// check that failed there: the log keeps only its end, where a long failure message can push out
// the name of the test that failed.
const checkLogName = "check.log"

// saveCheckOutput writes a failed check's whole output to logName (checkLogName, or setupLogName for
// the setup) in the worktree wt's .orchestra/run/, left for review with it, and returns the file's
// path. The worker can change .orchestra/run/, so the file
// is reached through an os.Root, as every run file is (see project.OpenRun). A file that can't be
// written is logged, and the log's path returned: it has the end of the output.
func (o *Loop) saveCheckOutput(wt, logName string, out []byte) string {
	rel := project.RunPath(logName)
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

// checkFail is what a ticket's failed check said, for the reviewer: the run report says what
// failed, and whether in code the ticket changed, without sending the maintainer to the log.
type checkFail struct {
	br, onto string   // the branch checked, rebased onto Base at onto
	how      string   // as checkHow says
	output   string   // where the whole output is, as saveCheckOutput returned it
	said     []string // the lines of the output that say what failed (see failureLines)
	saidEnd  bool     // no line said: said is the output's last lines, if it printed any
	dirs     []string // the directories the ticket's own commits change, sorted (see ownDirs)
	setup    string   // the setup command, when it failed before the check could run (see setUp)
}

// noteCheckFailed keeps for the reviewer what w's check said as it failed with err, or its setup when
// setup is the setup command: the lines of its output, out, that say what failed, where out is kept
// (output), and the directories that the ticket's commits on top of onto change.
func (o *Loop) noteCheckFailed(ctx context.Context, w worker, onto, output string, out []byte, err error,
	setup string) {
	f := checkFail{br: w.br, onto: onto, how: o.checkHow(err), output: output, setup: setup}
	if f.said = failureLines(string(out)); len(f.said) == 0 {
		f.saidEnd = true
		if end := lastLines(string(out), maxSaid); strings.TrimSpace(end) != "" {
			f.said = boundLines(strings.Split(end, "\n"), maxSaid)
		}
	}
	f.dirs = o.ownDirs(context.WithoutCancel(ctx), w.br, onto) // a read cut short would look like no change
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.checkSaid == nil {
		o.checkSaid = map[string]checkFail{}
	}
	o.checkSaid[w.id] = f
}

// checkSaidOf is what ticket id's last check in this run said, if it failed.
func (o *Loop) checkSaidOf(id string) (checkFail, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	f, ok := o.checkSaid[id]
	return f, ok
}

// ownDirs lists the directories of the files that the commits on br on top of onto change, sorted,
// "." for the repository's top level; nil when git can't say.
func (o *Loop) ownDirs(ctx context.Context, br, onto string) []string {
	if onto == "" {
		return nil
	}
	var dirs []string
	for _, f := range o.history.ChangedFiles(ctx, o.cfg.Repo, onto, br) {
		if d := path.Dir(f); !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	slices.Sort(dirs)
	return dirs
}

// How much of a failed check's output the reviewer is given: at most maxSaid lines, each cut to
// saidWidth runes.
const (
	maxSaid   = 30
	saidWidth = 200
)

var (
	// failedAnywhere matches a line of a check's output, trimmed, that says a package or a test failed
	// (go test's FAIL and --- FAIL:, gotestsum's === FAIL:, a bare FAIL saying nothing more aside), or
	// that the race detector found a data race, in the test --- FAIL: then names.
	failedAnywhere = regexp.MustCompile(`^(FAIL\W+\S|--- FAIL|=== FAIL)|DATA RACE|race detected`)
	// failedAtStart matches a line that, unindented, says what failed: a panic (a test that timed out),
	// a tool's own error, gotestsum's count of failures, golangci-lint's of issues, and a lint, vet or
	// build error, which starts with its file's position. A failing test's own messages are indented.
	failedAtStart = regexp.MustCompile(`^(panic:|(?i:error)\b|fatal:|level=error|DONE \d.*fail|\d+ issues?:|` +
		`[^\s:]+\.\w+:\d+(:\d+)?: )`)
)

// failureLines picks the lines of a failed check's output that say what failed, as failedAnywhere and
// failedAtStart match them, with the tests still running when go test timed out, which the lines
// after its "running tests:" name. Each is kept once, and at most maxSaid of them, the last saying
// how many more there are.
func failureLines(out string) []string {
	var said []string
	seen := map[string]bool{}
	running := false // in the tests named after "running tests:", until a blank line
	for line := range strings.Lines(out) {
		line = strings.TrimRight(line, "\r\n")
		t := strings.TrimSpace(line)
		switch {
		case t == "":
			running = false
			continue
		case t == "running tests:":
			running = true
		case !running && !failedAnywhere.MatchString(t) && !failedAtStart.MatchString(line):
			continue
		}
		if !seen[t] {
			seen[t] = true
			said = append(said, t)
		}
	}
	return boundLines(said, maxSaid)
}

// boundLines cuts each line to saidWidth runes and keeps at most n of them, the first; the last kept
// then says how many more there were.
func boundLines(lines []string, n int) []string {
	if len(lines) > n {
		more := len(lines) - n + 1
		lines = append(lines[:n-1:n-1], fmt.Sprintf("(… and %d more lines like these)", more))
	}
	for i, l := range lines {
		if r := []rune(l); len(r) > saidWidth {
			lines[i] = string(r[:saidWidth-1]) + "…"
		}
	}
	return lines
}

// flakyPrefix starts a line of a check's output that says a test failed and then passed on a rerun:
// a convention for any project's check, which the README documents.
const flakyPrefix = "FLAKY:"

// reportFlaky warns of each FLAKY: line in the output of a passing check, which was ticket id's when
// id is set, else what what says ("the full check 'scripts/check-full.sh'"): the check passed, so the
// ticket merges, but a test that passes only on a rerun is a bug to fix, and nobody would see it
// otherwise.
func (o *Loop) reportFlaky(id, what string, out []byte) {
	if id != "" {
		what = fmt.Sprintf("%s's check '%s'", id, o.cfg.Check)
	}
	for line := range strings.Lines(string(out)) {
		test, ok := strings.CutPrefix(strings.TrimRight(line, "\r\n"), flakyPrefix)
		if !ok {
			continue
		}
		if test = strings.TrimSpace(test); test == "" {
			test = "it doesn't say which"
		}
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  FLAKY: %s passed, but a test failed and then passed on a rerun: %s", what, test)})
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
