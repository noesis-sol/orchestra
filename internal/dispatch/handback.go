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
// commits of its own it had then.
type rebaseStop struct {
	worker
	onto, head string
	own        int
	files      []string
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
	return o.workerAway(ctx, r.id)
}

// workerAway says why ticket id's worker can't be handed its finished ticket back, or "" when it is
// idle in its tab.
func (o *Loop) workerAway(ctx context.Context, id string) string {
	switch st, err := o.agents.Status(ctx, o.agentName(id)); {
	case err != nil:
		return "its worker's status could not be read" + because(err)
	case st == StateGone:
		return "its worker is gone"
	case st != StateIdle && st != StateDone:
		return "its worker is " + string(st)
	}
	return ""
}

// resolvePrompt is what the worker is asked to do with a rebase stopped on conflicts.
func (o *Loop) resolvePrompt(r rebaseStop) string {
	c := o.cfg
	return fmt.Sprintf("%s moved on while you worked; rebasing %s onto it stopped on conflicts in %s. "+
		"Resolve them keeping the intent of both your change and what landed on %s, "+
		"then git add the files and run GIT_EDITOR=true git rebase --continue; "+
		"if the rebase stops on another commit, resolve that one the same way. "+
		"If %s has a commit's change already, wholly or as the part that conflicts, run git rebase --skip "+
		"for that commit instead of resolving it. "+
		"Once the rebase has finished, run '%s' in the foreground until it passes, since the commits that "+
		"applied cleanly may break it too; fold any fix into the branch's last commit with git commit --amend --no-edit. "+
		"Don't do anything else: no new commits beyond the rebase, no merge, no push, no ticket changes. "+
		"Say DONE when the rebase is complete and the check passes.",
		c.Base, r.br, strings.Join(r.files, ", "), c.Base, c.Base, c.Check)
}

// handBack gives a stopped rebase to the ticket's worker and waits for it to finish, then looks at
// the result itself. It returns why the resolution failed, or "" once the branch is on top of onto
// with the ticket's own commits, or those of them Base hasn't already, to be checked next (see
// checkRebased) or closed as unchanged when none are left, or errInterrupted after
// Ctrl+C (the rebase is left as it is). Neither lock is held: the worker may take minutes.
func (o *Loop) handBack(ctx context.Context, r rebaseStop) (string, *stopReason) {
	line := fmt.Sprintf("  RESOLVING: %s conflicts with %s in %s; handed back to its worker in tab %s to resolve "+
		"(up to %s)", r.br, o.cfg.Base, strings.Join(r.files, ", "), r.tab, command.ShortDuration(o.handBackLimit()))
	return o.handBackTo(ctx, r, handResolving, line, o.resolvePrompt(r), func() string {
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

// handBackTo hands r's ticket back to its idle worker as mode says, with line for the log and prompt
// for the worker, waits for it to settle (see waitResolved), and then returns why verify, which looks
// at what the worker left without taking its word for it, rejects that: "" when it doesn't. It returns
// why the hand-back failed before that, or errInterrupted after Ctrl+C, the worker's work left as it is.
// Neither lock is held: the worker may take minutes. The time limit, handBackLimit, runs from the
// hand-back, not from when the worker is seen to take it.
func (o *Loop) handBackTo(ctx context.Context, r rebaseStop, mode handMode, line, prompt string,
	verify func() string) (string, *stopReason) {
	agent := o.agentName(r.id)
	limit := o.handBackLimit()
	deadline := time.Now().Add(limit)
	w := o.newWatcher(r.wt, o.setHandedBack(r.id, mode))
	o.emit(Event{Kind: EvInfo, Ticket: r.id, Text: line})
	if !o.deliverPrompt(ctx, agent, prompt) {
		if ctx.Err() != nil {
			return "", errInterrupted
		}
		o.setHandedBack(r.id, handNone)
		return "its worker did not take the prompt", nil
	}
	why := o.waitResolved(ctx, agent, r.wt, deadline, limit, w.report)
	if ctx.Err() != nil {
		return "", errInterrupted
	}
	o.setHandedBack(r.id, handNone)
	if why != "" {
		return why, nil
	}
	return verify(), nil
}

// waitResolved waits for the worker to settle after its hand-back, which it took and has started
// on: idle with the rebase over, idle for idleGrace with it still in progress (its own background
// command may keep it idle), gone, or still busy at deadline, limit after the hand-back, which it
// returns as the reason. Each status read goes to report.
func (o *Loop) waitResolved(ctx context.Context, agent, wt string, deadline time.Time, limit time.Duration,
	report func(context.Context, AgentState, error)) string {
	var idleSince time.Time
	for {
		st, err := o.agents.Status(ctx, agent)
		report(ctx, st, err)
		if err != nil {
			o.log.Raw("", err)
		}
		idle := err == nil && (st == StateIdle || st == StateDone)
		switch {
		case err == nil && st == StateGone:
			return ""
		case !idle:
			idleSince = time.Time{}
		case idleSince.IsZero():
			idleSince = time.Now()
		}
		if idle && (!o.merger.RebaseInProgress(ctx, wt) || time.Since(idleSince) >= idleGrace) {
			return ""
		}
		if time.Now().After(deadline) {
			if idle {
				return "" // what it left is checked next
			}
			if err != nil {
				return fmt.Sprintf("its worker's status could still not be read after %s", command.ShortDuration(limit))
			}
			return fmt.Sprintf("its worker was still %s after %s", st, command.ShortDuration(limit))
		}
		if !sleep(ctx, o.pollEvery()) {
			return ""
		}
	}
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
	on := o.offBranch(keep, r.worker)
	switch {
	case o.merger.RebaseInProgress(keep, r.wt):
		return "its worker left the rebase unfinished"
	case o.checkout.DirtyWorktree(keep, r.wt) != "":
		return "its worker left uncommitted changes in " + r.wt
	case on != "":
		return o.leftOffBranch(r, on)
	case !o.merger.IsAncestor(keep, c.Repo, r.onto, r.br):
		return fmt.Sprintf("%s is not on top of %s as it was rebased onto", r.br, c.Base)
	case o.merger.Unchanged(keep, c.Repo, r.onto, r.br):
		return ""
	case o.merger.CommitNaming(keep, c.Repo, r.onto, r.br, r.id) == "":
		return fmt.Sprintf("no commit on %s names %s any more", r.br, r.id)
	}
	if n := o.merger.CountCommits(keep, c.Repo, r.onto+".."+r.br); n < 0 || n > r.own {
		return fmt.Sprintf("%s has %d commits where the ticket had %d (a commit made besides the rebase?)",
			r.br, n, r.own)
	}
	return ""
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
	err        error  // why the reset failed
}

// restoreBranch puts r's branch back at commit to after a hand-back to its worker is rejected: a rebase
// in progress aborted, and the branch reset to to when it is elsewhere and the worktree is clean and on
// it, as a reset in the worktree resets what it has checked out. The caller holds repoMu.
func (o *Loop) restoreBranch(ctx context.Context, r rebaseStop, to string) restored {
	x := restored{br: r.br, wt: r.wt, to: to}
	if o.merger.RebaseInProgress(ctx, r.wt) {
		if err := o.abortRebase(ctx, r.wt); err != nil {
			x.stuck = true
			return x
		}
		x.aborted = true
	}
	x.dirty = o.checkout.DirtyWorktree(ctx, r.wt) != ""
	x.now = o.checkout.Head(ctx, o.cfg.Repo, r.br)
	if x.now == to || x.dirty {
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
	resolving, fixing := mode == handResolving, mode == handFixing
	o.mu.Lock()
	st := o.active[id]
	st.Resolving, st.Fixing, st.Merge, st.Place = resolving, fixing, MergeNone, 0 // it joins the queue again after
	if st.Ticket != "" {
		o.active[id] = st
	}
	o.mu.Unlock()
	o.status(Status{Ticket: st.Ticket, Title: st.Title, Tab: st.Tab, Started: st.Started, Agent: StateIdle,
		Resolving: resolving, Fixing: fixing})
	return st
}
