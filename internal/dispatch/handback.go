package dispatch

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// maxHandBacks is how many times a finished ticket's rebase conflicts are handed back to its
// worker before the ticket is set aside for review.
const maxHandBacks = 2

// rebaseStop is a rebase stopped on conflicts in a finished ticket's worktree, as merge found it:
// the ticket's worker, the commit it was rebasing onto, the branch's commit before it and how many
// commits of its own it had then. The branch of a ticket checked before it merges, rebased or on top
// of Base already (onTop: onto is then Base's commit under it, and head the branch's own), is one too.
type rebaseStop struct {
	worker
	onto, head string
	own        int
	files      []string
	onTop      bool
}

// branchOn names r's branch as it is checked, for a line about it: "wt/A rebased onto main", or
// "wt/A on top of main" when it was on top of base already.
func (r rebaseStop) branchOn(base string) string {
	if r.onTop {
		return r.br + " on top of " + base
	}
	return r.br + " rebased onto " + base
}

// whyNotHandBack says why a stopped rebase can't be handed back to the ticket's worker, or "" if
// it can. The caller holds repoMu.
func (o *Loop) whyNotHandBack(ctx context.Context, r rebaseStop, handedBack int) string {
	c := o.cfg
	switch {
	case !c.ResolveConflicts:
		return "resolving conflicts is turned off"
	case c.Check == "":
		return "there is no check command in .orchestra/settings.json to check a resolution with"
	case handedBack >= maxHandBacks:
		return fmt.Sprintf("it was handed back to its worker %d times already", handedBack)
	case r.onto == "" || r.head == "" || r.own < 1 || len(r.files) == 0:
		return "git could not say what the rebase stopped on"
	}
	return o.workerAway(ctx, r.worker)
}

// workerAway says why the worker w can't be handed its finished ticket back, or "" when it is idle in
// its tab, or at the account's usage limit there: it is handed it back once the run's hold says it
// carries on (see waitResolved). One the terminal has no agent under its name for is looked for in its
// pane (see workerState).
func (o *Loop) workerAway(ctx context.Context, w worker) string {
	switch st, err := o.workerState(ctx, w); {
	case err != nil:
		return "its worker's status could not be read" + because(err)
	case st == StateGone:
		return "its worker is gone"
	case st != StateIdle && st != StateDone && st != StateLimited:
		return "its worker is " + string(st)
	}
	return ""
}

// resolvePrompt is what the worker is asked to do with a rebase stopped on conflicts; setup says when
// to run the project's setup, if it is to (see setupToResolve).
func (o *Loop) resolvePrompt(r rebaseStop, setup string) string {
	c := o.cfg
	return fmt.Sprintf("%s moved on while you worked; rebasing %s onto it stopped on conflicts in %s. "+
		"Resolve them keeping the intent of both your change and what landed on %s, "+
		"then git add the files and run GIT_EDITOR=true git rebase --continue; "+
		"if the rebase stops on another commit, resolve that one the same way. "+
		"If %s has a commit's change already, wholly or as the part that conflicts, run git rebase --skip "+
		"for that commit instead of resolving it. "+
		"%s"+
		"Once the rebase has finished, run the tests of the code you changed and of the files that conflicted "+
		"in the foreground until they pass, since the commits that applied cleanly may break them too; "+
		"fold any fix into the branch's last commit with git commit --amend --no-edit. "+
		"Leave the whole check, '%s', to orchestra: it runs it on the branch next. "+
		"Don't do anything else: no new commits beyond the rebase, no merge, no push, no ticket changes. "+
		"Say DONE when the rebase is complete and those tests pass.",
		c.Base, r.br, strings.Join(r.files, ", "), c.Base, c.Base, setup, c.Check)
}

// handBack gives a stopped rebase to the ticket's worker and waits for it to finish, then looks at
// the result itself. A rebase that brings in a change to a file the project's setup reads has the
// worker told to run the setup once it has finished, before its tests (see setupToResolve). It returns
// why the resolution failed, or "" once the branch is on top of onto with the ticket's own commits, or
// those of them Base hasn't already, to be checked next (see
// checkRebased) or closed as unchanged when none are left, or the reason the run stops meanwhile,
// errInterrupted after Ctrl+C or the usage limit's, the rebase left as it is (see handBackTo). Neither
// lock is held: the worker may take minutes.
func (o *Loop) handBack(ctx context.Context, r rebaseStop) (string, *stopReason) {
	setup, told := o.setupToResolve(context.WithoutCancel(ctx), r) // a read cut short would look like no change
	line := fmt.Sprintf("  RESOLVING: %s conflicts with %s in %s; handed back to its worker in tab %s to resolve "+
		"(up to %s)%s", r.br, o.cfg.Base, strings.Join(r.files, ", "), r.tab, command.ShortDuration(o.handBackLimit()),
		told)
	return o.handBackTo(ctx, r, handResolving, line, o.resolvePrompt(r, setup), r.head, func(bool) string {
		return o.verifyResolved(ctx, r)
	})
}

// handMode is what a ticket's worker is handed back, as the dashboard shows it.
type handMode int

const (
	handNone      handMode = iota // nothing, or nothing any longer
	handResolving                 // its rebase stopped on conflicts, to resolve (see handBack)
	handFixing                    // a check failing on its rebased branch, to fix (see fixCheck)
)

// handBackLimit is how long a worker may take over a hand-back, from when it is handed back.
func (o *Loop) handBackLimit() time.Duration {
	return orDefault(o.cfg.ResolveTimeout, project.DefaultResolveTimeout)
}

// handBackTo hands r's ticket back to its worker as mode says, with line for the log and prompt for
// the worker, waits for it to settle (see waitResolved), and then returns why verify, which looks at
// what the worker left without taking its word for it, rejects that: "" when it doesn't. A worker at
// the account's usage limit once handed back, with what git has done already as verify, told the
// worker is unsettled, finds it (the rebase finished, with the branch on top of Base, or a fix
// committed), has given its answer: its turn ended on the limit as it finished, as tutti-nrg.7.7's
// did, and the branch goes on to be checked and merged without waiting for the limit to lift. It returns
// why the hand-back failed before that, or the reason the run stops meanwhile, the worker's work left
// as it is and the ticket handed back still: errInterrupted after Ctrl+C, or the usage limit's (see
// limitStop). Neither lock is held: the worker may take minutes, or wait out the usage limit. The time
// limit, handBackLimit, runs from the hand-back, not from when the worker is seen to take it, and
// leaves out the time it waits on the usage limit, and the time the machine is loaded, up to the limit
// again (see loadWait). A worker still at it past the limit is stopped and told what becomes of its
// work (see endLate): verify, told the worker is unsettled, says whether that is done already; to is the
// commit the caller puts the branch back at when the hand-back fails.
func (o *Loop) handBackTo(ctx context.Context, r rebaseStop, mode handMode, line, prompt, to string,
	verify func(unsettled bool) string) (string, *stopReason) {
	limit := o.handBackLimit()
	deadline := time.Now().Add(limit)
	watch := o.newWatcher(r.wt, o.setHandedBack(r.id, mode))
	o.emit(Event{Kind: EvInfo, Ticket: r.id, Text: line})
	taken := false
	answered := func() bool {
		if verify(true) != "" {
			return false
		}
		o.info("  %s's worker is at the usage limit, but %s: %s is taken as it is, without waiting for the limit "+
			"to lift", r.id, handBackDone(mode), r.br)
		taken = true
		return true
	}
	why, late, stop := o.waitResolved(ctx, r.worker, prompt, carryOnHandBack(r, mode, o.cfg.Base), deadline, limit,
		answered, watch.report)
	if stop == nil && ctx.Err() != nil {
		stop = errInterrupted
	}
	if stop != nil {
		return "", stop
	}
	o.setHandedBack(r.id, handNone)
	switch {
	case taken:
		return "", nil // verify passed as it was taken
	case late:
		return o.endLate(ctx, r, mode, to, why, verify(true)), nil
	case why != "":
		return why, nil
	}
	return verify(false), nil
}

// handBackDone says what the worker on a hand-back as mode says has done when git shows it done.
func handBackDone(mode handMode) string {
	if mode == handResolving {
		return "its rebase has finished"
	}
	return "its fix is committed"
}

// carryOnHandBack is what the limited worker on r's ticket, handed back as mode says onto base, is told
// to carry on with once the usage limit has reset (see tellAfterLimit): the hand-back, not the ticket,
// which it closed already, and to say DONE when it had finished it.
func carryOnHandBack(r rebaseStop, mode handMode, base string) string {
	what := fmt.Sprintf("the failing check orchestra handed back to you, fixing %s so that it passes and "+
		"committing the fix", r.br)
	if mode == handResolving {
		what = fmt.Sprintf("the rebase of %s onto %s orchestra handed back to you, resolving its conflicts, "+
			"finishing it and running the tests", r.br, base)
	}
	return fmt.Sprintf("Orchestra: the account's usage limit has reset. Carry on where you left off with %s, "+
		"as orchestra's message asked, not with %s itself, which you closed already. If you had finished that "+
		"before the limit, check that git shows it done and say DONE.", what, r.id)
}

// endLate ends the hand-back of r's ticket to its worker, still at it as the time limit passed, as why
// says, and returns why the hand-back failed, or "" when what git has is done already, as verify found
// it at the limit (rejected says why not): the rebase finished, with the branch on top of Base, or a fix
// committed. The limit runs on the clock, the machine's load extending it at most twofold, and a worker
// may be past it with its work committed, running the tests after (tutti-nrg.5.9's had committed its
// resolution 9 minutes before). Either way its turn is stopped, with Escape as at the usage limit, and
// it is told: that its branch goes on as it is to be checked and merged, or that the hand-back is over
// and the branch put back at to, its attempt named, before the caller puts it back (see undoResolution,
// undoFix), so that it doesn't go on working on a ticket set aside.
func (o *Loop) endLate(ctx context.Context, r rebaseStop, mode handMode, to, why, rejected string) string {
	keep := context.WithoutCancel(ctx) // a read cut short would name no commit
	limit := command.ShortDuration(o.handBackLimit())
	if rejected == "" {
		at, err := o.checkout.Head(keep, o.cfg.Repo, r.br)
		o.log.Raw("", err)
		o.info("  %s's worker was still at its hand-back after %s, but %s: %s is taken as it is", r.id, limit,
			handBackDone(mode), r.br)
		o.interruptWith(ctx, r.worker, handBackTaken(r, mode, limit, at, o.cfg.Check))
		return ""
	}
	attempt := o.attempt(keep, r, to)
	o.interruptWith(ctx, r.worker, handBackOver(r, mode, limit, to, attempt))
	o.info("  %s's worker was stopped and told its hand-back is over", r.id)
	return why
}

// attempt is the commit r's worker got to on its hand-back, its worktree's HEAD, as the branch is to
// be put back at to: "" when it is there, or at the commit the branch was rebased onto, the worker
// having committed nothing of its attempt, or when git can't say.
func (o *Loop) attempt(ctx context.Context, r rebaseStop, to string) string {
	head, err := o.checkout.Head(ctx, r.wt, "HEAD")
	o.log.Raw("", err)
	if head == to || head == r.onto {
		return ""
	}
	return head
}

// interruptWith stops the turn of the worker w, still at its hand-back past the time limit, with
// Escape, as tellAfterLimit does, and gives it msg.
func (o *Loop) interruptWith(ctx context.Context, w worker, msg string) {
	if err := o.agents.SendKeys(ctx, w.agent, "esc"); err != nil {
		o.log.Raw("", fmt.Errorf("cannot press Escape for %s: %w", w.agent, err))
	}
	if !sleep(ctx, escapeSettle) {
		return
	}
	if !o.deliverPrompt(ctx, w.agent, msg) && ctx.Err() == nil {
		o.log.Raw("", fmt.Errorf("%s's worker did not take the message that its hand-back is over", w.id))
	}
}

// handBackTaken is what the worker on r's ticket is told when its hand-back as mode says is past the
// time limit, limit, with what it was asked done already: its branch, at commit at, is taken as it is,
// to be checked with check and merged.
func handBackTaken(r rebaseStop, mode handMode, limit, at, check string) string {
	what := "fix is committed"
	if mode == handResolving {
		what = "rebase has finished"
	}
	return fmt.Sprintf("Orchestra: the %s for this hand-back is up, and your %s, so orchestra has stopped you "+
		"and takes %s as it is now, at %s, to check with '%s' and merge. Leave %s as it is: no more commits, "+
		"no amending, no git commands in it, no ticket changes. Say DONE.",
		limit, what, r.br, short(at), check, r.wt)
}

// handBackOver is what the worker on r's ticket is told when its hand-back as mode says is past the
// time limit, limit, and not done: the hand-back is over, the branch put back at to and the ticket set
// aside, its attempt (see attempt) kept in git.
func handBackOver(r rebaseStop, mode handMode, limit, to, attempt string) string {
	back := fmt.Sprintf("puts %s back at %s, where the check failed,", r.br, short(to))
	if mode == handResolving {
		back = fmt.Sprintf("aborts the rebase if it is still in progress, puts %s back at %s, where it was "+
			"before the rebase,", r.br, short(to))
	}
	kept := "Nothing of your attempt was committed, so none of it is kept."
	if attempt != "" {
		kept = fmt.Sprintf("Your attempt so far is commit %s, which git keeps (git show %s).", attempt, attempt)
	}
	return fmt.Sprintf("Orchestra: the %s for this hand-back is up, so orchestra has stopped you and ends it: "+
		"it %s and sets %s aside for review. %s Don't work on it any more: no git commands in %s, no ticket "+
		"changes. Say DONE.", limit, back, r.id, kept, r.wt)
}

// waitResolved hands the worker w its finished ticket back with prompt and waits for it to settle once
// it has started on it: idle with the rebase over, idle for idleGrace with it still in progress (its
// own background command may keep it idle), gone, or still busy at deadline, limit after the
// hand-back, which it returns as the reason with late set, as it does a prompt not taken without. A
// worker at the account's usage limit, as it is handed back or later, joins the run's hold (see
// limitPoll), the time it waits not counting toward limit: one limited as it is handed back is given
// prompt where the hold tells it to carry on, or once it goes idle by itself, and one limited later is
// told to carry on with carryOn. A worker limited once handed back settles all the same when answered,
// asked once each time it is seen limited (git can't change under a turn that has ended), says git
// has its answer. It returns the reason the run stops meanwhile: errInterrupted after Ctrl+C, or the
// hold's once it has lasted Config.UsageLimitWait. The time the machine is loaded meanwhile, the worker
// not limited, doesn't count toward limit either, up to limit (see loadWait). Each status read goes to
// report; one the terminal has no agent under its name for is looked for in its pane (see workerState).
func (o *Loop) waitResolved(ctx context.Context, w worker, prompt, carryOn string, deadline time.Time,
	limit time.Duration, answered func() bool, report func(context.Context, AgentState, error)) (
	why string, late bool, stop *stopReason) {
	var lw limitWait
	var ld loadWait
	asked := false // answered was asked since the worker was last seen limited, or told
	var idleSince, settled time.Time
	defer func() { o.usage.done(w.id, &lw, settled, stop) }()
	given := false
	give := func() string {
		if !o.deliverPrompt(ctx, w.agent, prompt) {
			return "its worker did not take the prompt"
		}
		given = true
		return ""
	}
	if st, err := o.workerState(ctx, w); err == nil && st == StateLimited {
		o.info("  %s's worker is at the usage limit: it is handed back once the limit lifts, the wait not counting "+
			"toward the %s", w.id, command.ShortDuration(limit))
	} else if why := give(); why != "" {
		return why, false, nil
	}
	for polled := false; ; polled = true {
		if polled && !sleep(ctx, o.pollEvery()) || ctx.Err() != nil {
			return "", false, errInterrupted
		}
		st, err := o.workerState(ctx, w)
		report(ctx, st, err)
		ld.note(time.Now(), (err != nil || st != StateLimited) && o.loaded())
		switch {
		case err != nil:
			o.log.Raw("", err)
		case st == StateGone:
			lw.leave(time.Now(), st)
			if !given {
				return "its worker is gone", false, nil
			}
			return "", false, nil
		default:
			var h hooksSeen
			msg := carryOn
			if st == StateLimited {
				h = o.hooksOf(w) // when its limit resets, as they say
			}
			if !given {
				msg = prompt
			}
			stop, told := o.limitPoll(ctx, w, st, h, &lw, msg)
			switch {
			case stop != nil:
				return "", false, stop
			case told:
				given, idleSince, asked = true, time.Time{}, false
				continue // the checks below wait for its next poll
			case st != StateLimited:
				asked = false
			case given && !asked:
				if asked = true; answered() {
					settled = time.Now()
					return "", false, nil
				}
			}
		}
		idle := err == nil && (st == StateIdle || st == StateDone)
		if idle && !given { // past the limit by itself: handed back now
			if why := give(); why != "" {
				return why, false, nil
			}
			continue
		}
		switch {
		case !idle:
			idleSince = time.Time{}
		case idleSince.IsZero():
			idleSince = time.Now()
		}
		// A rebase git can't tell is over is waited for as one in progress: its worker may be at it still.
		if idle && (!o.rebasing(ctx, w.wt) || time.Since(idleSince) >= idleGrace) {
			settled = time.Now()
			return "", false, nil
		}
		if now := time.Now(); now.After(deadline.Add(lw.limited(now) + ld.held(now, limit))) {
			if idle {
				settled = now
				return "", false, nil // what it left is checked next
			}
			after := command.ShortDuration(limit)
			if l := ld.held(now, limit); l > 0 {
				after += fmt.Sprintf(" (and %s more for the machine's load)", command.ShortDuration(l.Round(time.Second)))
			}
			if err != nil {
				return fmt.Sprintf("its worker's status could still not be read after %s", after), true, nil
			}
			return fmt.Sprintf("its worker was still %s after %s", st, after), true, nil
		}
	}
}

// handBackLeft says what a stop of the run leaves under way of ticket id's hand-back to its worker,
// for the maintainer to finish, as a line or a note on it puts it: "" when it has none.
func (o *Loop) handBackLeft(id string) string {
	o.mu.Lock()
	st := o.active[id]
	o.mu.Unlock()
	switch st.Phase {
	case PhaseResolving:
		return "its rebase, handed back to resolve, is left in progress: once its worker has finished it, " +
			"run the check and merge the branch by hand"
	case PhaseFixing:
		return "its failing check, handed back to fix, is left to its worker: once it has committed the fix, " +
			"run the check and merge the branch by hand"
	}
	return ""
}

// verifyResolved checks, without taking the worker's word for it, that the rebase is over, the
// worktree clean and on the branch, and the branch on top of onto with the ticket's own commits and
// nothing more: as many as it had, or fewer where its worker skipped those whose change Base has
// already. A branch left with no change of its own, every commit skipped or what is left changing
// nothing, passes too, to be closed as unchanged (see merge). It returns why not; the check runs next
// (see checkRebased), on what the worktree has checked out, and the merge then brings in the branch.
func (o *Loop) verifyResolved(ctx context.Context, r rebaseStop) string {
	c := o.cfg
	keep := context.WithoutCancel(ctx) // a read cut short would look like a failed resolution
	if why := o.leftClean(keep, r, "the rebase unfinished"); why != "" {
		return why
	}
	switch onTop, err := o.merger.IsAncestor(keep, c.Repo, r.onto, r.br); {
	case err != nil:
		return gitCantTell(fmt.Sprintf("whether %s is on top of %s", r.br, c.Base), err)
	case !onTop:
		return fmt.Sprintf("%s is not on top of %s as it was rebased onto", r.br, c.Base)
	}
	switch unchanged, err := o.merger.Unchanged(keep, c.Repo, r.onto, r.br); {
	case err != nil:
		return gitCantTell(fmt.Sprintf("whether the commits on %s change anything", r.br), err)
	case unchanged:
		return ""
	}
	switch naming, n, err := o.merger.OwnCommits(keep, c.Repo, r.onto, r.br, r.id); {
	case err != nil:
		return gitCantTell("what its worker left on "+r.br, err)
	case naming == "":
		return fmt.Sprintf("no commit on %s names %s any more", r.br, r.id)
	case n > r.own:
		return fmt.Sprintf("%s has %d commits where the ticket had %d (a commit made besides the rebase?)",
			r.br, n, r.own)
	}
	return ""
}

// leftClean says why what r's worker left after a hand-back is rejected when its worktree isn't as a
// hand-back must leave it, or "": no rebase in progress (rebasing says how one left reads, as in "the
// rebase unfinished"), no uncommitted changes, and on its branch; or git can't tell.
func (o *Loop) leftClean(ctx context.Context, r rebaseStop, rebasing string) string {
	switch in, err := o.merger.RebaseInProgress(ctx, r.wt); {
	case err != nil:
		return gitCantTell("whether its worker left a rebase in progress in "+r.wt, err)
	case in:
		return "its worker left " + rebasing
	}
	switch dirty, err := o.checkout.DirtyWorktree(ctx, r.wt); {
	case err != nil:
		return gitCantTell("whether its worker left uncommitted changes in "+r.wt, err)
	case dirty != "":
		return "its worker left uncommitted changes in " + r.wt
	}
	if on := o.offBranch(ctx, r.worker); on != "" {
		return o.leftOffBranch(r, on)
	}
	return ""
}

// rebasing reports whether a rebase is in progress in wt, or git can't tell, logging git's error: one
// can't be shown to be over.
func (o *Loop) rebasing(ctx context.Context, wt string) bool {
	in, err := o.merger.RebaseInProgress(ctx, wt)
	o.log.Raw("", err)
	return in || err != nil
}

// leftOffBranch says why what r's worker left after a hand-back is rejected, its worktree having on
// (see offBranch) rather than r's branch checked out: the check would run on that, and the merge bring
// in the branch unchecked.
func (o *Loop) leftOffBranch(r rebaseStop, on string) string {
	return fmt.Sprintf("its worker left %s with %s, not %s", r.wt, on, r.br)
}

// undoResolution puts the branch back as the ticket closed it after a failed hand-back (see
// restoreBranch) and says what it did. The caller holds repoMu.
func (o *Loop) undoResolution(ctx context.Context, r rebaseStop) string {
	return o.restoreBranch(ctx, r, r.head).afterResolution()
}

// restored is what restoreBranch did with a ticket's branch to put it back at commit to, for the
// caller to say.
type restored struct {
	br, wt, to string
	stuck      bool   // a rebase in progress could not be aborted, so nothing more was done
	aborted    bool   // a rebase in progress was aborted
	dirty      bool   // the worktree has changes, so the branch was left where it is
	off        string // what the worktree has checked out instead of the branch (see offBranch), left as it is
	now        string // where the branch was, after any abort; reset to to unless it was there, dirty or off
	unknown    error  // git could not tell what the worktree has, so the branch was left where it is
	err        error  // why the reset failed
}

// restoreBranch puts r's branch back at commit to after a hand-back to its worker is rejected: a rebase
// in progress aborted, and the branch reset to to when it is elsewhere and the worktree is clean and on
// it, as a reset in the worktree resets what it has checked out. The caller holds repoMu.
func (o *Loop) restoreBranch(ctx context.Context, r rebaseStop, to string) restored {
	x := restored{br: r.br, wt: r.wt, to: to}
	switch in, err := o.merger.RebaseInProgress(ctx, r.wt); {
	case err != nil:
		x.unknown = err
		return x
	case in:
		if err := o.abortRebase(ctx, r.wt); err != nil {
			x.stuck = true
			return x
		}
		x.aborted = true
	}
	dirty, err := o.checkout.DirtyWorktree(ctx, r.wt)
	x.dirty = dirty != ""
	if x.unknown = err; err == nil {
		x.now, x.unknown = o.checkout.Head(ctx, o.cfg.Repo, r.br)
	}
	if x.unknown != nil || x.now == to || x.dirty {
		return x
	}
	if x.off = o.offBranch(ctx, r.worker); x.off != "" {
		return x
	}
	var out string
	out, x.err = o.merger.ResetBranch(ctx, r.wt, to)
	o.log.Raw(out, x.err)
	return x
}

// afterResolution says what restoreBranch did after a rejected resolution of the rebase: to is the
// branch's commit before the rebase.
func (x restored) afterResolution() string {
	switch {
	case x.stuck:
		return "the rebase could not be aborted, so " + x.wt + " is left mid-rebase"
	case x.unknown != nil:
		return fmt.Sprintf("git could not say what %s has%s, so %s is left as its worker left it (it was at %s "+
			"before the rebase)", x.wt, because(x.unknown), x.br, short(x.to))
	case x.aborted && x.now == x.to:
		return "the rebase was aborted"
	case x.now == x.to:
		return x.br + " is as the ticket closed it"
	case x.dirty:
		return fmt.Sprintf("%s is left as its worker left it (it was at %s before the rebase)", x.br, short(x.to))
	case x.off != "":
		return fmt.Sprintf("%s is left at %s (it was at %s before the rebase), as %s has %s",
			x.br, short(x.now), short(x.to), x.wt, x.off)
	case x.err != nil:
		return fmt.Sprintf("%s could not be reset to %s, where it was before the rebase; it is left at %s",
			x.br, short(x.to), short(x.now))
	}
	return fmt.Sprintf("%s was reset to %s, where it was before the rebase (its worker's attempt is %s)",
		x.br, short(x.to), short(x.now))
}

func short(hash string) string {
	if len(hash) > 10 {
		return hash[:10]
	}
	return hash
}

// setHandedBack marks a running ticket as handed back to its worker as mode says, or no longer,
// shows that on the dashboard at once, and returns its status as the dashboard shows it. Its worker is
// idle then: before it takes the hand-back, and once it is done with it.
func (o *Loop) setHandedBack(id string, mode handMode) Status {
	phase := map[handMode]Phase{handNone: PhaseWorking, handResolving: PhaseResolving, handFixing: PhaseFixing}[mode]
	var st Status
	o.updateActive(id, func(s *Status) {
		if s.Merge != MergeNone {
			s.Activity = "" // the merge queue's words: its worker's own come with its next read
		}
		s.enter(phase, 0) // out of the merge queue: it joins it again after
		o.clocks.enter(id, phase, time.Now())
		s.Agent, s.Doing, s.Permission, s.Unreadable = StateIdle, "", false, false
		st = *s
	})
	return st
}
