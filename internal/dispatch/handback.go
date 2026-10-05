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
		"Resolve them keeping the intent of both your change and what landed on %s. "+
		"Run '%s' in the foreground until it passes, then git add the files and run GIT_EDITOR=true git rebase --continue; "+
		"if the rebase stops on another commit, resolve that one the same way. "+
		"Don't do anything else: no new commits beyond the rebase, no merge, no push, no ticket changes. "+
		"Say DONE when the rebase is complete.",
		c.Base, r.br, strings.Join(r.files, ", "), c.Base, c.Check)
}

// handBack gives a stopped rebase to the ticket's worker and waits for it to finish, then looks at
// the result itself. It returns why the resolution failed, or "" once the branch is on top of onto
// with the ticket's own commits, to be checked next (see checkRebased), or errInterrupted after
// Ctrl+C (the rebase is left as it is). Neither lock is held: the worker may take minutes. The resolve timeout
// runs from the hand-back, not from when the worker is seen to take it.
func (o *Loop) handBack(ctx context.Context, r rebaseStop) (string, *stopReason) {
	c := o.cfg
	agent := o.agentName(r.id)
	limit := orDefault(c.ResolveTimeout, project.DefaultResolveTimeout)
	deadline := time.Now().Add(limit)
	base := o.setHandedBack(r.id, true, false)
	w := o.newWatcher(r.wt, base)
	o.emit(Event{Kind: EvInfo, Ticket: r.id, Text: fmt.Sprintf(
		"  RESOLVING: %s conflicts with %s in %s; handed back to its worker in tab %s to resolve (up to %s)",
		r.br, c.Base, strings.Join(r.files, ", "), r.tab, command.ShortDuration(limit))})
	if !o.deliverPrompt(ctx, agent, o.resolvePrompt(r)) {
		if ctx.Err() != nil {
			return "", errInterrupted
		}
		o.setHandedBack(r.id, false, false)
		return "its worker did not take the prompt", nil
	}
	why := o.waitResolved(ctx, agent, r.wt, deadline, limit, w.report)
	if ctx.Err() != nil {
		return "", errInterrupted
	}
	o.setHandedBack(r.id, false, false)
	if why != "" {
		return why, nil
	}
	return o.verifyResolved(ctx, r), nil
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
// worktree clean, and the branch on top of onto with the ticket's own commits and nothing more. It
// returns why not; the check runs next (see checkRebased).
func (o *Loop) verifyResolved(ctx context.Context, r rebaseStop) string {
	c := o.cfg
	keep := context.WithoutCancel(ctx) // a read cut short would look like a failed resolution
	switch {
	case o.merger.RebaseInProgress(keep, r.wt):
		return "its worker left the rebase unfinished"
	case o.checkout.DirtyWorktree(keep, r.wt) != "":
		return "its worker left uncommitted changes in " + r.wt
	case !o.merger.IsAncestor(keep, c.Repo, r.onto, r.br):
		return fmt.Sprintf("%s is not on top of %s as it was rebased onto", r.br, c.Base)
	case o.merger.CommitNaming(keep, c.Repo, r.onto, r.br, r.id) == "":
		return fmt.Sprintf("no commit on %s names %s any more", r.br, r.id)
	}
	if n := o.merger.CountCommits(keep, c.Repo, r.onto+".."+r.br); n != r.own {
		return fmt.Sprintf("%s has %d commits where the ticket had %d (a commit made besides the rebase?)",
			r.br, n, r.own)
	}
	return ""
}

// undoResolution puts the branch back as the ticket closed it after a failed hand-back: the
// rebase aborted, or, finished but rejected, the branch reset to its earlier commit when the
// worktree is clean. It says what it did. The caller holds repoMu.
func (o *Loop) undoResolution(ctx context.Context, r rebaseStop) string {
	c := o.cfg
	if o.merger.RebaseInProgress(ctx, r.wt) {
		if err := o.abortRebase(ctx, r.wt); err != nil {
			return "the rebase could not be aborted, so " + r.wt + " is left mid-rebase"
		}
		return "the rebase was aborted"
	}
	now := o.checkout.Head(ctx, c.Repo, r.br)
	if now == r.head {
		return r.br + " is as the ticket closed it"
	}
	if o.checkout.DirtyWorktree(ctx, r.wt) != "" {
		return fmt.Sprintf("%s is left as its worker left it (it was at %s before the rebase)", r.br, short(r.head))
	}
	out, err := o.merger.ResetBranch(ctx, r.wt, r.head)
	o.log.Raw(out, err)
	if err != nil {
		return fmt.Sprintf("%s could not be reset to %s, where it was before the rebase; it is left at %s",
			r.br, short(r.head), short(now))
	}
	return fmt.Sprintf("%s was reset to %s, where it was before the rebase (its worker's attempt is %s)",
		r.br, short(r.head), short(now))
}

func short(hash string) string {
	if len(hash) > 10 {
		return hash[:10]
	}
	return hash
}

// setHandedBack marks a running ticket as having its rebase handed back to its worker to resolve, or
// its failed check to fix, or neither any longer, shows that on the dashboard at once, and returns its
// status as the dashboard shows it. Its worker is idle then: before it takes the hand-back, and once
// it is done with it.
func (o *Loop) setHandedBack(id string, resolving, fixing bool) Status {
	o.mu.Lock()
	st := o.active[id]
	st.Resolving, st.Fixing = resolving, fixing
	if st.Ticket != "" {
		o.active[id] = st
	}
	o.mu.Unlock()
	o.status(Status{Ticket: st.Ticket, Title: st.Title, Tab: st.Tab, Started: st.Started, Agent: StateIdle,
		Resolving: resolving, Fixing: fixing})
	return st
}
