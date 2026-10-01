package dispatch

import (
	"context"
	"fmt"
	"path/filepath"
	"time"
)

// lateAdopt is how long, after Herdr's own adoption gave up, the loop keeps watching a pane the
// worker was launched in: 2 minutes.
const lateAdopt = 2 * time.Minute

// adoptLate watches a pane a worker was launched in for its agent, and names it agent once one of
// the configured kind is there. When it gives up, held says what the pane holds ("" for nothing).
// The error is a rename Herdr refused, or the context ending.
func (o *Loop) adoptLate(ctx context.Context, pane, agent string) (held string, adopted bool, err error) {
	deadline := time.Now().Add(orDefault(o.wait.adopt, lateAdopt))
	for {
		name, kind, st, readErr := o.namer.PaneAgent(ctx, pane)
		switch {
		case readErr != nil:
			const why = "Herdr could not say what the pane holds"
			if why != held {
				o.log.Raw("", readErr)
			}
			held = why
		case st == StateGone:
			held = ""
		case kind != o.cfg.AgentKind:
			held = fmt.Sprintf("the pane holds a %s agent", kind)
		case name == agent:
			return "", true, nil
		default:
			err := o.namer.RenameAgent(ctx, pane, agent)
			if err == nil {
				return "", true, nil
			}
			if o.starter.IsNameRefused(err) {
				return "", false, err
			}
			why := fmt.Sprintf("the %s agent in the pane could not be renamed: %v", kind, err)
			if why != held {
				o.log.Raw("", err)
			}
			held = why
		}
		if time.Now().After(deadline) {
			return held, false, nil
		}
		if !sleep(ctx, o.pollEvery()) {
			return held, false, ctx.Err()
		}
	}
}

// prepareWorktree creates the ticket's worktree, or reuses the one left by an earlier attempt and
// brings its branch up to date, holding the repository lock. conflicts reports a branch that could
// not be rebased onto Base.
func (o *Loop) prepareWorktree(ctx context.Context, id, br string) (wt string, conflicts bool, stop *stopReason) {
	c := o.cfg
	defer o.markFinishing(id, "worktree setup")()
	o.repoMu.Lock()
	defer o.repoMu.Unlock()
	o.log.Raw(o.worktrees.Prune(ctx, c.Repo)) // forget a worktree whose folder was deleted, so it isn't reused
	if wt := o.worktrees.WorktreeOf(ctx, c.Repo, br); wt != "" {
		o.info("  reusing worktree %s (%s)", wt, br)
		return wt, !o.refreshBranch(ctx, wt, br), nil
	}
	wt = filepath.Join(c.WTRoot, id)
	var out string
	var err error
	if o.worktrees.HasBranch(ctx, c.Repo, br) {
		out, err = o.worktrees.AddWorktree(ctx, c.Repo, wt, br)
	} else {
		out, err = o.worktrees.NewWorktree(ctx, c.Repo, wt, br, c.Base)
	}
	o.log.Raw(out, err)
	if err != nil {
		return "", false, halt(ExitTool, stopWorktreeFailed, " for %s at %s (git output is in %s)",
			id, wt, c.LogPath).causedBy(err)
	}
	o.info("  worktree %s on %s", wt, br)
	return wt, !o.refreshBranch(ctx, wt, br), nil
}

// agentName is the Herdr name of ticket id's worker; without a Namer (in tests), the ID itself.
func (o *Loop) agentName(id string) string {
	if o.namer == nil {
		return id
	}
	return o.namer.AgentName(id)
}

// promptTaken confirms the worker started on its prompt. Given at launch, it counts as taken once
// the worker works, has claimed the ticket, or shows any action; otherwise (or if it did not take)
// it is delivered by pasting.
func (o *Loop) promptTaken(ctx context.Context, id, agent, prompt string, atLaunch bool) bool {
	if atLaunch {
		if o.agents.WaitStarted(ctx, agent) || o.claimed(ctx, id) || lastActivity(o.agents.Screen(ctx, agent, "")) != "" {
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		o.log.Raw("", fmt.Errorf("%s did not start on the prompt given at launch; pasting it", id))
	}
	return o.deliverPrompt(ctx, agent, prompt)
}

// claimed reports whether the worker has taken its ticket out of "open". A status bd can't give
// doesn't count.
func (o *Loop) claimed(ctx context.Context, id string) bool {
	st, err := o.tickets.Status(ctx, id)
	if err != nil {
		o.log.Raw("", err)
		return false
	}
	return st != "open"
}

// deliverPrompt sends the worker its prompt and confirms it started on it. 'herdr agent prompt'
// can paste the text without the Enter registering, leaving the worker idle with the prompt in
// its input box; the worker then looks settled and its ticket would be deferred untouched.
// Enter is pressed only when the box visibly holds the prompt (never on a dialog, where it would
// pick an option), and the prompt is sent again only when the box is empty, so never twice.
func (o *Loop) deliverPrompt(ctx context.Context, agent, prompt string) bool {
	for attempt := 1; attempt <= 2; attempt++ {
		err := o.agents.Prompt(ctx, agent, prompt)
		if err == nil {
			return true // herdr saw the worker start
		}
		o.log.Raw("", err)
		if ctx.Err() != nil {
			return false
		}
		st, err := o.readStatus(ctx, agent, 5)
		if err != nil {
			o.log.Raw("", err)
			return false // not started, as far as anyone can tell
		}
		switch st {
		case StateWorking, StateBlocked:
			return true // it started; a block is handled by the settle loop
		case StateIdle, StateDone:
			if inputHolds(o.agents.Screen(ctx, agent, st), prompt) {
				if err := o.agents.SendKeys(ctx, agent, "enter"); err != nil {
					o.log.Raw("", fmt.Errorf("cannot press Enter for %s: %w", agent, err))
				}
				return o.agents.WaitStarted(ctx, agent)
			}
			// The box is empty: the paste itself was lost, so send it again.
		default:
			return false
		}
	}
	return false
}
