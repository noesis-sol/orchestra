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
	deadline := time.Now().Add(lateAdopt)
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

// startRetry is how long a failed 'agent start' is given, before the pane is looked at for the
// agent, which may still be coming up.
const startRetry = 3 * time.Second

// startedWorker is the worker startWorker started for a ticket.
type startedWorker struct {
	tab      string    // the Herdr tab it runs in
	started  time.Time // when its tab was opened
	atLaunch bool      // its prompt was given at launch, from its prompt file
	hooks    bool      // it reports what it does through hooks
}

// startWorker opens a tab in worktree wt for ticket t's worker and starts it there, named agent:
// from its prompt file launch if it has one and that works, or else with herdr agent start, its
// prompt then to be pasted. mcpArgs give it its MCP servers and report its hooks; it does without
// the hooks if Herdr refuses them. It shows the ticket as starting in that tab, and clears that
// again unless it returns the worker started, which leaves clearing it to the caller.
func (o *Loop) startWorker(ctx context.Context, t Ticket, agent, wt string, mcpArgs, report []string,
	launch string) (startedWorker, *stopReason) {
	c := o.cfg
	id := t.ID
	tab, pane, err := o.tabs.CreateTab(ctx, c.Workspace, wt, id)
	if ctx.Err() != nil {
		if err == nil {
			o.closeTab(context.WithoutCancel(ctx), tab)
		}
		return startedWorker{}, errInterrupted
	}
	if err != nil {
		return startedWorker{}, halt(ExitTool, stopTabFailed, " for %s (is '%s' a valid workspace?)",
			id, c.Workspace).causedBy(err)
	}
	started := time.Now()
	o.setActive(Status{Ticket: id, Title: t.Title, Tab: tab, Started: started})
	o.status(Status{Ticket: id, Title: t.Title, Tab: tab, Started: started, Agent: "starting"})
	up := false
	defer func() {
		if !up { // also when it panics
			o.status(Status{Ticket: id, Gone: true})
		}
	}()

	// startArgs are the worker's arguments: its MCP servers and the run's arguments for every Claude
	// worker (--no-chrome or --chrome, --effort), which it always gets, then its reports and its prompt, if it
	// has them.
	fixed := mcpArgs[:len(mcpArgs):len(mcpArgs)]
	if c.AgentKind == "claude" {
		fixed = append(fixed, c.WorkerArgs...)
	}
	startArgs := func() []string {
		args := append(append([]string{}, fixed...), report...)
		if launch != "" {
			args = append(args, launch)
		}
		return args
	}

	// With its prompt in a file, the worker is started by typing the command into the tab and
	// named once Herdr recognises it. Herdr's own start waits for the agent to look ready for
	// input, which a worker that goes straight to work never does, so it could only time out.
	// A name Herdr refuses would be refused on every retry, so that ends the attempt at once.
	nameRefused := func(err error) *stopReason {
		return halt(ExitTool, stopStartFailed,
			" for %s in tab %s: Herdr refused the agent name %s (%v)", id, tab, agent, err).causedBy(err)
	}
	ok := false
	if launch != "" {
		err := o.starter.LaunchInPane(ctx, pane, c.AgentKind, startArgs())
		typed := err == nil
		if typed {
			_, err = o.namer.AdoptAgent(ctx, pane, c.AgentKind, agent)
		}
		if ctx.Err() != nil {
			return startedWorker{}, errInterrupted
		}
		if o.starter.IsNameRefused(err) {
			return startedWorker{}, nameRefused(err)
		}
		if ok = err == nil; !ok && typed {
			// The typed command may still be starting (a slow first start, many MCP servers), and
			// a second agent started in its pane would take the prompt twice: watch the pane for
			// longer, and start another only once it is plainly empty.
			o.log.Raw("", fmt.Errorf(
				"%s's worker was not named %s after it was launched (%v); watching its tab for longer", id, agent, err))
			held, adopted, err := o.adoptLate(ctx, pane, agent)
			if ctx.Err() != nil {
				return startedWorker{}, errInterrupted
			}
			if o.starter.IsNameRefused(err) {
				return startedWorker{}, nameRefused(err)
			}
			if ok = adopted; ok {
				o.info("  %s's worker was slow to start; named it %s", id, agent)
			} else if held != "" {
				return startedWorker{}, halt(ExitTool, stopStartFailed,
					" for %s in tab %s: the worker launched there could not be named %s (%s); "+
						"stopping rather than starting a second one in it",
					id, tab, agent, held)
			}
		}
		if !ok {
			o.log.Raw("", fmt.Errorf("%s's worker could not be started from its prompt file and named %s (%v); "+
				"starting it with herdr agent start and pasting the prompt", id, agent, err))
			launch = ""
		}
	}

	// 'agent start' can report failure while the agent is still coming up (agent_not_ready keeps
	// the name), and a retry then finds the pane occupied, so after each failure check whether
	// the agent is there before trying again. The prompt is pasted once it has started.
	for attempt := 0; attempt < 10 && !ok; attempt++ {
		args := startArgs()
		err := o.starter.StartAgent(ctx, agent, c.AgentKind, pane, args)
		if ok = err == nil; ok {
			break
		}
		if o.starter.IsNameRefused(err) {
			return startedWorker{}, nameRefused(err)
		}
		o.log.Raw("", err)
		if o.starter.IsArgumentRefused(err) && len(args) > len(fixed) {
			report = nil // start it plainly, without reports
			continue
		}
		if o.starter.IsArgumentRefused(err) && len(mcpArgs) > 0 {
			// Without them it would start with every MCP server on the machine.
			return startedWorker{}, halt(ExitTool, stopStartFailed,
				" for %s in tab %s: Herdr refused the arguments giving it its MCP servers (%v)", id, tab, err).causedBy(err)
		}
		if !sleep(ctx, startRetry) {
			return startedWorker{}, errInterrupted
		}
		st, err := o.agents.Status(ctx, agent)
		if err != nil {
			o.log.Raw("", err)
		}
		if err == nil && st == StateGone {
			// A start that times out leaves the agent running unnamed in its pane, and a retry
			// would find the pane busy: adopt that agent under the ticket's name instead.
			name, kind, pst, perr := o.namer.PaneAgent(ctx, pane)
			if perr != nil {
				o.log.Raw("", perr)
			}
			if perr == nil && pst != StateGone && name == "" && kind == c.AgentKind {
				if err := o.namer.RenameAgent(ctx, pane, agent); err == nil {
					o.info("  %s's worker started without its name; named it %s", id, agent)
					st = pst
				} else {
					if o.starter.IsNameRefused(err) {
						return startedWorker{}, nameRefused(err)
					}
					o.log.Raw("", err)
				}
			}
		}
		switch st {
		case StateIdle, StateDone:
			ok = true
		case StateWorking, StateBlocked, StateUnknown:
			// Up but busy (a startup dialog, say): give it time rather than starting a second one.
			ok = o.starter.WaitReady(ctx, agent)
		}
	}
	if !ok {
		return startedWorker{}, halt(ExitTool, stopStartFailed, " for %s in tab %s", id, tab)
	}
	up = true
	return startedWorker{tab: tab, started: started, atLaunch: launch != "", hooks: report != nil}, nil
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

// deliverPrompt sends the worker its prompt and confirms it started on it, returning as soon as it
// has (or plainly has not): it does not wait for the turn to end, so the caller watches the worker
// through it. 'herdr agent prompt' can paste the text without the Enter registering, leaving the
// worker idle with the prompt in its input box; the worker then looks settled and its ticket would
// be deferred untouched. Enter is pressed only when the box visibly holds the prompt (never on a
// dialog, where it would pick an option), and the prompt is sent again only when the box is empty,
// so never twice.
func (o *Loop) deliverPrompt(ctx context.Context, agent, prompt string) bool {
	for attempt := 1; attempt <= 2; attempt++ {
		err := o.agents.Prompt(ctx, agent, prompt)
		if err == nil {
			return true // herdr saw the worker start: working, or blocked
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
