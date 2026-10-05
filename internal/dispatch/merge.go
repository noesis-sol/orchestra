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
		o.closedUnchanged(keep, w)
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
// review and the run goes on; a failing check is tried once more after Base moves on (see recheck).
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
					": %s does not fast-forward onto %s; worktree %s and tab %s left for review", br, c.Base, wt, tab).
					blocks("does not fast-forward onto " + c.Base)
			}
			o.merged(keep, id)
			o.countMerge()
			cleaned := o.removeWorktree(keep, wt, br)
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
		if output, err := o.runCheck(ctx, w, r.onto); err != nil {
			if ctx.Err() != nil {
				return errInterrupted
			}
			o.leaveUnmerged(keep, id, "CHECKS_FAILED")
			how, why := o.checkHow(err), "checks failed"
			if errors.Is(err, errCheckTimedOut) {
				why = "checks timed out"
			}
			// Checked once more once Base moves on, unless this was that once (see recheck).
			again, later := " again", ""
			if !o.isRechecked(id) {
				again, later = "", o.awaitRecheck(keep, w, r.onto)
			}
			o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: why + again, Text: fmt.Sprintf(
				"  CHECKS_FAILED: %s closed, but '%s' %s%s on %s rebased onto %s; "+
					"worktree %s and tab %s left for review (output is in %s)%s",
				id, c.Check, how, again, br, c.Base, wt, tab, output, later)})
			return nil
		}
		o.info("  '%s' passes on the rebased %s", c.Check, br)
		// Lock again and merge; if Base moved once more meanwhile, rebase and check again.
	}
	o.leaveUnmerged(keep, id, "MERGE_CONFLICT")
	o.emit(Event{Kind: EvWarn, Ticket: id, Aside: true, Detail: c.Base + " kept changing",
		Blocked: c.Base + " kept changing", Text: fmt.Sprintf(
			"  MERGE_CONFLICT: %s closed, but %s kept changing while its checks ran (commits made by hand?); "+
				"worktree %s and tab %s left for review", id, c.Base, wt, tab)})
	return nil
}

// closedUnchanged cleans up after w's ticket, closed with no change of its own: its branch has no
// commits beyond Base and its worktree is clean, as when tickets merged before it did what it was
// about. Nothing is left to merge or to review, so its worktree, branch and tab are removed as after
// a merge, and the tickets it blocks needn't wait for it.
func (o *Loop) closedUnchanged(ctx context.Context, w worker) {
	c := o.cfg
	id, br, wt, tab := w.id, w.br, w.wt, w.tab
	o.merged(ctx, id) // an unmerged label from an earlier run holds nothing now
	o.repoMu.Lock()
	cleaned := o.removeWorktree(ctx, wt, br)
	o.repoMu.Unlock()
	title := o.titleOf(id)
	if !cleaned {
		o.emit(Event{Kind: EvClosed, Ticket: id, Title: title, Detail: "no change; cleanup failed, tab " + tab + " left open",
			Text: fmt.Sprintf("  %s closed with no change to merge, but CLEANUP_FAILED for %s / %s (git output is in %s); "+
				"tab %s left open", id, wt, br, c.LogPath, tab)})
		return
	}
	o.emit(Event{Kind: EvClosed, Ticket: id, Title: title, Detail: "no change to merge", Text: fmt.Sprintf(
		"  %s closed with no change: %s has no commits beyond %s and its worktree is clean, so there is nothing to merge; %s",
		id, br, c.Base, o.closeWorkerTab(ctx, id, tab))})
}

// removeWorktree removes a ticket's worktree wt and then its branch br, whose work is on Base, and
// reports whether both went; git's output goes in the log. The caller holds repoMu.
func (o *Loop) removeWorktree(ctx context.Context, wt, br string) bool {
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
	limit := o.checkTimeout()
	checkCtx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	out, err := command.GroupOutput(checkCtx, 5*time.Second, w.wt, "sh", "-c", o.cfg.Check)
	if err != nil && ctx.Err() == nil && errors.Is(checkCtx.Err(), context.DeadlineExceeded) {
		err = fmt.Errorf("%w: stopped after %s (%v)", errCheckTimedOut, command.ShortDuration(limit), err)
	}
	if err != nil {
		o.log.Raw(lastLines(string(out), 40), fmt.Errorf("check '%s' in %s: %w", o.cfg.Check, w.wt, err))
		output := o.saveCheckOutput(w.wt, out)
		if ctx.Err() == nil { // stopped by Ctrl+C, it says nothing of the ticket
			o.noteCheckFailed(ctx, w, onto, output, out, err)
		}
		return output, err
	}
	o.reportFlaky(w.id, "", out)
	return "", nil
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

// checkFail is what a ticket's failed check said, for the reviewer: the run report says what
// failed, and whether in code the ticket changed, without sending the maintainer to the log.
type checkFail struct {
	br, onto string   // the branch checked, rebased onto Base at onto
	how      string   // as checkHow says
	output   string   // where the whole output is, as saveCheckOutput returned it
	said     []string // the lines of the output that say what failed (see failureLines)
	saidEnd  bool     // no line said: said is the output's last lines, if it printed any
	dirs     []string // the directories the ticket's own commits change, sorted (see ownDirs)
}

// noteCheckFailed keeps for the reviewer what w's check said as it failed with err: the lines of its
// output, out, that say what failed, where out is kept (output), and the directories that the
// ticket's commits on top of onto change.
func (o *Loop) noteCheckFailed(ctx context.Context, w worker, onto, output string, out []byte, err error) {
	f := checkFail{br: w.br, onto: onto, how: o.checkHow(err), output: output}
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
