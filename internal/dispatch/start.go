package dispatch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
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
			const why = "the terminal could not say what the pane holds"
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
			if errors.Is(err, ErrNameRefused) {
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

// nameLeft names agent the worker in pane, in ticket id's tab, whose start Ctrl+C cut short before it
// was named: the next run looks for it by that name. Herdr may not have recognised it yet; it then
// stays unnamed, and the next run, finding no agent by that name, looks for it in its pane (see
// earlierState).
func (o *Loop) nameLeft(ctx context.Context, id, tab, pane, agent string) {
	name, kind, st, err := o.namer.PaneAgent(ctx, pane)
	switch {
	case err != nil:
		o.log.Raw("", err)
	case st == StateGone:
		o.log.Raw("", fmt.Errorf("%s's start was cut short before the terminal saw a worker in tab %s to name %s: "+
			"one still coming up there is left unnamed, for the next run to look for in its pane", id, tab, agent))
	case kind != o.cfg.AgentKind || name == agent:
	default:
		if err := o.namer.RenameAgent(ctx, pane, agent); err != nil {
			o.log.Raw("", fmt.Errorf("cannot give %s's worker in tab %s the name %s as the run stops: %w", id, tab, agent, err))
		}
	}
}

// preparedWorktree is a ticket's worktree as prepareWorktree left it: where it is, and whether it is
// new, or else its branch's commit before it was brought up to date with Base.
type preparedWorktree struct {
	wt     string
	fresh  bool   // made now: nothing is installed in it
	cut    bool   // its branch too, from Base: on top of it, with nothing of an earlier attempt
	before string // a reused one's branch's commit before its rebase onto Base; "" when git can't say
}

// prepareWorktree creates the ticket's worktree, or reuses the one left by an earlier attempt and
// brings its branch up to date, holding the repository lock. conflicts reports a branch that could
// not be rebased onto Base.
func (o *Loop) prepareWorktree(ctx context.Context, id, br string) (p preparedWorktree, conflicts bool,
	stop *stopReason) {
	c := o.cfg
	defer o.markFinishing(id, finishWorktree)()
	o.repoMu.Lock()
	defer o.repoMu.Unlock()
	o.log.Raw(o.worktrees.Prune(ctx, c.Repo)) // forget a worktree whose folder was deleted, so it isn't reused
	// When git can't list the worktrees, none is reused, and adding one fails with git's reason.
	if wt, _ := o.worktrees.WorktreeOf(ctx, c.Repo, br); wt != "" {
		o.info("  reusing worktree %s (%s)", wt, br)
		p = preparedWorktree{wt: wt, before: o.checkout.Head(ctx, c.Repo, br)}
		return p, !o.refreshBranch(ctx, wt, br), nil
	}
	has, err := o.worktrees.HasBranch(ctx, c.Repo, br)
	p = preparedWorktree{wt: filepath.Join(c.WTRoot, id), fresh: true, cut: !has}
	if err != nil { // cut or reused, the worktree could leave out an earlier attempt's work or fail
		return preparedWorktree{}, false, halt(ExitTool, stopWorktreeFailed,
			" for %s at %s: git can't tell whether %s exists%s", id, p.wt, br, because(err)).causedBy(err)
	}
	var out string
	if p.cut {
		out, err = o.worktrees.NewWorktree(ctx, c.Repo, p.wt, br, c.Base)
	} else {
		out, err = o.worktrees.AddWorktree(ctx, c.Repo, p.wt, br)
	}
	o.log.Raw(out, err)
	if err != nil {
		return preparedWorktree{}, false, halt(ExitTool, stopWorktreeFailed, " for %s at %s (git output is in %s)",
			id, p.wt, c.LogPath).causedBy(err)
	}
	o.info("  worktree %s on %s", p.wt, br)
	if p.cut {
		return p, false, nil // on top of Base, which it was cut from as the repository lock is held
	}
	return p, !o.refreshBranch(ctx, p.wt, br), nil
}

// earlierNote tells a new worker what an earlier attempt at its ticket left on branch br and in
// worktree wt: commits Base doesn't have, uncommitted changes, or both. A worker told it is in its
// own worktree takes the branch for a fresh one, and would redo that work, commit it unread or
// discard it. It is "" when there is nothing, as in a new worktree.
func (o *Loop) earlierNote(ctx context.Context, br, wt string) string {
	base := o.cfg.Base
	var left []string
	switch n := o.merger.CountCommits(ctx, o.cfg.Repo, base+".."+br); {
	case n == 1:
		left = append(left, fmt.Sprintf("1 commit that %s doesn't have (git log %s..HEAD)", base, base))
	case n > 1:
		left = append(left, fmt.Sprintf("%d commits that %s doesn't have (git log %s..HEAD)", n, base, base))
	}
	if o.checkout.DirtyWorktree(ctx, wt) != "" {
		left = append(left, "uncommitted changes (git status)")
	}
	if len(left) == 0 {
		return ""
	}
	return fmt.Sprintf("\n\nAn earlier attempt at this ticket left work on %s: %s. Read it before anything else, "+
		"then build on it, or revert it deliberately; don't start over, and don't commit or discard it unread.\n",
		br, strings.Join(left, " and "))
}

// startRetry is how long a failed 'agent start' is given, before the pane is looked at for the
// agent, which may still be coming up.
const startRetry = 3 * time.Second

// startedWorker is the worker startWorker started for a ticket.
type startedWorker struct {
	tab      string    // the Herdr tab it runs in
	pane     string    // the pane in it
	started  time.Time // when its tab was opened
	atLaunch bool      // its prompt was given at launch, from its prompt file
	hooks    bool      // it reports what it does through hooks
	rules    bool      // its standing rules are in its system prompt
}

// workerArgs are the arguments a worker is started with besides the run's: they give it its MCP
// servers, its standing rules in its system prompt (see rulesArgs) and its reporting hooks.
type workerArgs struct {
	mcp, rules, report []string
}

// startWorker opens a tab in worktree wt for ticket t's worker and starts it there, named agent:
// from its prompt file launch if it has one and that works, or else through the terminal's
// StartAgent, its prompt then to be pasted. It does without its rules and hooks if the terminal
// refuses them. With a
// session (a Claude Code session ID), it resumes that session rather than starting a new one. It
// shows the ticket as starting in that tab, and clears that again unless it returns the worker
// started, which leaves clearing it to the caller. It places the worker as soon as the tab is open,
// and leaves it placed however the start ends.
func (o *Loop) startWorker(ctx context.Context, t Ticket, agent, wt string, a workerArgs,
	launch, session string) (startedWorker, *stopReason) {
	c := o.cfg
	id := t.ID
	tab, pane, err := o.tabs.CreateTab(ctx, c.Workspace, wt, id)
	if ctx.Err() != nil {
		if err == nil {
			o.closeTab(context.WithoutCancel(ctx), tab)
		} else {
			o.log.Raw("", err) // it says what became of a tab Herdr may have opened
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
	s := &workerStart{o: o, id: id, tab: tab, pane: pane, wt: wt, agent: agent,
		mcpArgs: a.mcp, fixed: slices.Clip(a.mcp), rules: a.rules, report: a.report, launch: launch}
	if c.ClaudeWorkers() {
		s.fixed = append(append(append(s.fixed, c.WorkerArgs...), o.permission...), o.unusedTools...)
	}
	if session != "" {
		s.fixed = append(s.fixed, "--resume", session)
	}
	// From here on a worker may be running in the tab, at work on its prompt, even if Ctrl+C or a
	// failure ends the start: placed at once, it is carried over to the next run (see saveCarried).
	s.place()
	launched, stop := s.fromPromptFile(ctx)
	if !launched && stop == nil {
		stop = s.startPlainly(ctx)
	}
	if stop != nil {
		return startedWorker{}, stop
	}
	up = true
	return startedWorker{tab: tab, pane: pane, started: started, atLaunch: s.launch != "", hooks: s.report != nil,
		rules: s.rules != nil}, nil
}

// workerStart is one start of ticket id's worker, named agent, in pane of tab, which startWorker
// takes step by step. A step goes on without the worker's rules and hooks once the terminal refuses them,
// and without its prompt file launch once starting from it fails.
type workerStart struct {
	o                        *Loop
	id, tab, pane, wt, agent string
	mcpArgs                  []string // the arguments giving it its MCP servers
	fixed                    []string // the arguments it always gets: mcpArgs, the run's for every Claude worker, --resume
	rules                    []string // the arguments giving it its standing rules; nil once the terminal refused them
	report                   []string // the arguments for its hooks; nil once the terminal refused them
	launch                   string   // its prompt file launch; "" once it is to be started with StartAgent
}

// place places the worker where it is being started, with or without its hooks.
func (s *workerStart) place() {
	s.o.place(s.id, workerSpot{tab: s.tab, pane: s.pane, wt: s.wt, hooks: s.report != nil})
}

// args are the worker's arguments: its MCP servers, the run's arguments for every Claude worker
// (--no-chrome or --chrome, --effort, --permission-mode, --disallowedTools) and the session it resumes, which it always
// gets, then its standing rules, its reports and its prompt, if it has them.
func (s *workerStart) args() []string {
	args := append(append(append([]string{}, s.fixed...), s.rules...), s.report...)
	if s.launch != "" {
		args = append(args, s.launch)
	}
	return args
}

// interrupted ends a start cut short once a worker may have been launched: it names the worker if
// the terminal has it in the pane, so the next run, which looks for it by name, finds it.
func (s *workerStart) interrupted(ctx context.Context) *stopReason {
	s.o.nameLeft(context.WithoutCancel(ctx), s.id, s.tab, s.pane, s.agent)
	return errInterrupted
}

// nameRefused ends the start if err is the terminal refusing the agent name, which it would refuse on
// every retry; for any other error, or none, it is nil.
func (s *workerStart) nameRefused(err error) *stopReason {
	if !errors.Is(err, ErrNameRefused) {
		return nil
	}
	return halt(ExitTool, stopStartFailed,
		" for %s in tab %s: the terminal refused the agent name %s (%v)", s.id, s.tab, s.agent, err).causedBy(err)
}

// fromPromptFile starts the worker, if it has a prompt file launch, by typing the command into the
// tab, and names it once Herdr recognises it. Herdr's own start waits for the agent to look ready
// for input, which a worker that goes straight to work never does, so it could only time out.
// launched is false, with no stop, when the worker is to be started with StartAgent instead.
func (s *workerStart) fromPromptFile(ctx context.Context) (launched bool, stop *stopReason) {
	if s.launch == "" {
		return false, nil
	}
	o, c := s.o, s.o.cfg
	err := o.starter.LaunchInPane(ctx, s.pane, c.AgentKind, s.args())
	typed := err == nil
	if typed {
		_, err = o.namer.AdoptAgent(ctx, s.pane, c.AgentKind, s.agent)
	}
	if ctx.Err() != nil {
		return false, s.interrupted(ctx)
	}
	if stop := s.nameRefused(err); stop != nil {
		return false, stop
	}
	if err == nil {
		return true, nil
	}
	if typed {
		if launched, stop := s.waitLaunched(ctx, err); launched || stop != nil {
			return launched, stop
		}
	}
	o.log.Raw("", fmt.Errorf("%s's worker could not be started from its prompt file and named %s (%v); "+
		"starting it plainly and pasting the prompt", s.id, s.agent, err))
	s.launch = ""
	return false, nil
}

// waitLaunched watches the pane for longer after the command typed there was not named (why): it
// may still be starting (a slow first start, many MCP servers), and a second agent started in its
// pane would take the prompt twice. launched is false, with no stop, only once the pane is plainly
// empty, for another to be started there.
func (s *workerStart) waitLaunched(ctx context.Context, why error) (launched bool, stop *stopReason) {
	o := s.o
	o.log.Raw("", fmt.Errorf(
		"%s's worker was not named %s after it was launched (%v); watching its tab for longer", s.id, s.agent, why))
	held, adopted, err := o.adoptLate(ctx, s.pane, s.agent)
	if ctx.Err() != nil {
		return false, s.interrupted(ctx)
	}
	if stop := s.nameRefused(err); stop != nil {
		return false, stop
	}
	if adopted {
		o.info("  %s's worker was slow to start; named it %s", s.id, s.agent)
		return true, nil
	}
	if held != "" {
		return false, halt(ExitTool, stopStartFailed,
			" for %s in tab %s: the worker launched there could not be named %s (%s); "+
				"stopping rather than starting a second one in it",
			s.id, s.tab, s.agent, held)
	}
	return false, nil
}

// startPlainly starts the worker with the terminal's StartAgent, its prompt then to be pasted, in
// up to 10 attempts; arguments the terminal refuses are dropped where they can be, and stop the
// start where they can't. A start can report failure while the agent is still coming up (Herdr's
// agent_not_ready keeps the name), and a retry then finds the pane occupied, so after each failure
// it checks whether the agent is there before trying again.
func (s *workerStart) startPlainly(ctx context.Context) *stopReason {
	o := s.o
	for range 10 {
		args := s.args()
		err := o.starter.StartAgent(ctx, s.agent, o.cfg.AgentKind, s.pane, args)
		if err == nil {
			return nil
		}
		if stop := s.nameRefused(err); stop != nil {
			return stop
		}
		o.log.Raw("", err)
		if errors.Is(err, ErrArgumentRefused) && len(args) > len(s.fixed) {
			s.rules, s.report = nil, nil // start it plainly: its rules are pasted with its prompt
			s.place()
			continue
		}
		if errors.Is(err, ErrArgumentRefused) && len(s.mcpArgs) > 0 {
			// Without them it would start with every MCP server on the machine.
			return halt(ExitTool, stopStartFailed,
				" for %s in tab %s: the terminal refused the arguments giving it its MCP servers (%v)",
				s.id, s.tab, err).causedBy(err)
		}
		if errors.Is(err, ErrArgumentRefused) {
			// Nothing is left to drop, and the same arguments would be refused on every try.
			return halt(ExitTool, stopStartFailed, " for %s in tab %s: the terminal refused the arguments %q (%v)",
				s.id, s.tab, strings.Join(args, " "), err).causedBy(err)
		}
		if !sleep(ctx, startRetry) {
			return s.interrupted(ctx)
		}
		if up, stop := s.cameUp(ctx); up || stop != nil {
			return stop
		}
	}
	return halt(ExitTool, stopStartFailed, " for %s in tab %s", s.id, s.tab)
}

// cameUp reports whether the worker is up after a failed 'agent start', adopting it if it runs
// unnamed in its pane; one up but busy (a startup dialog, say) is given time rather than a second
// one started.
func (s *workerStart) cameUp(ctx context.Context) (bool, *stopReason) {
	o := s.o
	st, err := o.agents.Status(ctx, s.agent)
	if err != nil {
		o.log.Raw("", err)
	}
	if err == nil && st == StateGone {
		var stop *stopReason
		if st, stop = s.adoptUnnamed(ctx); stop != nil {
			return false, stop
		}
	}
	switch st {
	case StateIdle, StateDone:
		return true, nil
	case StateWorking, StateBlocked, StateUnknown:
		return o.starter.WaitReady(ctx, s.agent), nil
	}
	return false, nil
}

// adoptUnnamed names the agent of the configured kind running unnamed in the pane, as a start that
// times out leaves it (a retry would find the pane busy), and returns its state: StateGone if there
// is none, or it could not be named.
func (s *workerStart) adoptUnnamed(ctx context.Context) (AgentState, *stopReason) {
	o := s.o
	name, kind, st, err := o.namer.PaneAgent(ctx, s.pane)
	if err != nil {
		o.log.Raw("", err)
		return StateGone, nil
	}
	if st == StateGone || name != "" || kind != o.cfg.AgentKind {
		return StateGone, nil
	}
	err = o.namer.RenameAgent(ctx, s.pane, s.agent)
	if err == nil {
		o.info("  %s's worker started without its name; named it %s", s.id, s.agent)
		return st, nil
	}
	if stop := s.nameRefused(err); stop != nil {
		return StateGone, stop
	}
	o.log.Raw("", err)
	return StateGone, nil
}

// agentName is the agent name of ticket id's worker; without a Namer (in tests), the ID itself.
func (o *Loop) agentName(id string) string {
	if o.namer == nil {
		return id
	}
	return o.namer.AgentName(id)
}

// promptTaken confirms the worker started on its prompt. Given at launch, it counts as taken once
// the worker works, has claimed the ticket, or shows any action; otherwise (or if it did not take)
// it is delivered by pasting. A resumed worker's ticket was claimed by the worker before it, and its
// screen shows that worker's actions, so for it only its starting to work counts.
func (o *Loop) promptTaken(ctx context.Context, id, agent, prompt string, atLaunch, resumed bool) bool {
	if atLaunch {
		if o.agents.WaitStarted(ctx, agent) ||
			!resumed && (o.claimed(ctx, id) || lastActivity(o.agents.Screen(ctx, agent, "")) != "") {
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
	return st != StatusOpen
}

// deliverPrompt sends the worker its prompt and confirms it started on it, returning as soon as it
// has (or plainly has not): it does not wait for the turn to end, so the caller watches the worker
// through it. 'herdr agent prompt' can paste the text without the Enter registering, leaving the
// worker idle with the prompt in its input box; the worker then looks settled and its ticket would
// be deferred untouched. Enter is pressed only when the box visibly holds the prompt, or what the
// terminal typed for it (typedPrompt), never on a dialog, where it would pick an option; the prompt
// is sent again only when the box is empty, so never twice.
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
			if inputHolds(o.agents.Screen(ctx, agent, st), o.typedPrompt(ctx, agent, prompt)) {
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

// typedPrompt is what the terminal's Prompt puts in the agent's input box for prompt (PromptTyper),
// which deliverPrompt looks for there: by default, the prompt itself.
func (o *Loop) typedPrompt(ctx context.Context, agent, prompt string) string {
	if t, ok := o.agents.(PromptTyper); ok {
		return t.TypedPrompt(ctx, agent, prompt)
	}
	return prompt
}

// refreshBranch rebases a returning ticket's branch onto Base, which has moved on since the branch
// was cut, so its worker starts from current code. A failed rebase is undone and it returns false:
// the branch conflicts with Base.
func (o *Loop) refreshBranch(ctx context.Context, wt, br string) bool {
	c := o.cfg
	// Where git can't tell, the branch is rebased, which leaves one on top of Base as it is.
	if on, _ := o.merger.IsAncestor(ctx, c.Repo, c.Base, br); on {
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
