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

// earlierAction is what work does with a returning ticket's earlier worker (see earlierStep).
type earlierAction int

const (
	leaveEarlier   earlierAction = iota // none is left: nothing to do
	adoptEarlier                        // adopt it: it carries the ticket on
	tellEarlier                         // tell it to carry on, and adopt it if it does
	stopForEarlier                      // stop the run: it is at work, and a second worker must not start beside it
	closeEarlier                        // close its tab: the new worker takes up its worktree
	renameEarlier                       // rename it, for the new worker to have its name
)

// earlierStep is what work does with a returning ticket's earlier worker in state st, asked saying
// the ticket is back from a question asked in this run. One waiting on the usage limit is adopted:
// it carries on after the reset, told to or by itself, and a second worker beside it would too. One
// at work, or that the terminal can't tell the state of (which may be at work, as settling counts it:
// see waitSettled), is adopted when it was left by a question, and stops the run otherwise. One left
// idle or done by a question is told to carry on, and any other idle or done has its tab closed (see
// closeEarlier). Any other still there is renamed.
func earlierStep(st AgentState, asked bool) earlierAction {
	switch st {
	case StateGone:
		return leaveEarlier
	case StateLimited:
		return adoptEarlier
	case StateWorking, StateBlocked, StateUnknown:
		if asked {
			return adoptEarlier
		}
		return stopForEarlier
	case StateIdle, StateDone:
		if asked {
			return tellEarlier
		}
		return closeEarlier
	}
	return renameEarlier
}

// stillIn says where an earlier worker in state st still is, where (its tab), and what it is
// doing there when the terminal can tell.
func stillIn(st AgentState, where string) string {
	if st == StateUnknown {
		return "in " + where + ", though the terminal can't tell what it is doing"
	}
	return fmt.Sprintf("%s in %s", st, where)
}

// ticketRun is one ticket's run, from its worker's start or adoption to its outcome: the ticket,
// its branch, its worker's name, the context for what must finish once begun, and how the worker
// settled, which launch hands to Run for the hold for the environment.
type ticketRun struct {
	o             *Loop
	t             Ticket
	id, br, agent string
	// What must finish once begun (making the worktree, merging, the notes on what happened) runs on
	// keep, which Ctrl+C doesn't cancel, so the repository and the ticket aren't left half done.
	// Each command still has its time limit.
	keep context.Context
	how  settling // how the worker settled, set as it does
	cut  bool     // its branch was cut from Base for this worker: no earlier attempt left anything on it
}

// newTicketRun is ticket t's run, its worker started or adopted on ctx.
func (o *Loop) newTicketRun(ctx context.Context, t Ticket) *ticketRun {
	return &ticketRun{o: o, t: t, id: t.ID, br: branchOf(t.ID),
		// The worker's Herdr name: Herdr takes fewer characters than a ticket ID can hold.
		agent: o.agentName(t.ID),
		keep:  context.WithoutCancel(ctx)}
}

// work runs one ticket from worktree to merge. It returns a reason when the run must stop; the
// ticket then stays listed as active (still being worked on), or, back from a question before its
// earlier worker is told anything, asked. It sets r.how to how the worker settled, for the hold
// for the environment. A panic in it is such a reason, PANIC, which launch, running it, recovers.
func (r *ticketRun) work(ctx context.Context) *stopReason {
	o, id := r.o, r.id
	// Back from a question, the ticket stays asked, for a stop or Ctrl+C to leave it as it was (see
	// leaveAsked, saveCarried), until its earlier worker is told to carry on or adopted (see takeOn),
	// or a new worker is to take it.
	earlier, asked := o.asked(id)
	if !asked {
		earlier, _ = o.leftOpenSpot(id) // set aside with its worker left in its tab: where that is
	}
	o.book.unfollow(id) // dispatched again: its earlier worker is adopted or replaced below
	if HasLabel(r.t, UnmergedLabel) {
		o.setLabelled(id, true) // reopened after an earlier run left it unmerged: merging removes the label
	}
	if ctx.Err() != nil {
		return errInterrupted // Ctrl+C came as it was dispatched
	}
	st, done, stop := r.takeUpEarlier(ctx, earlier, asked)
	if done {
		return stop
	}
	o.book.answer(id) // back from a question: set aside again once this worker settles, it stays out
	p, done, stop := r.prepare()
	if done {
		return stop
	}
	r.cut = p.cut
	// The worker starts with the project's dependencies installed (see setUpWorktree).
	if done, stop := r.setUpWorktree(ctx, p); done {
		return stop
	}
	wt := p.wt
	// Back from a question with its earlier worker gone from its tab, the ticket has that worker's
	// session resumed, once in a run, rather than a new worker starting over (see resume.go).
	if asked && st == StateGone {
		if s, ok := o.sessionOf(wt, earlier.hooks); ok && o.book.resumeOnce(id) {
			return r.resumeSession(ctx, wt, earlier.tab, earlier.question, backFromQuestion, s)
		}
	}
	return r.start(ctx, wt)
}

// takeUpEarlier deals with a returning ticket's earlier worker, earlier, asked saying the ticket is
// back from a question asked in this run (see earlierStep). It may still sit in its tab under the
// ticket's name, which Herdr keeps unique, or, carried over from a run cut short as it started it,
// unnamed in its pane (see earlierState). It is looked at before the worktree is touched: one still
// at work there must not have its branch rebased under it. One left by a question asked in this run
// may have had its answer in its tab and carried on: it is adopted, or, idle, told the answer is in.
// One waiting on the usage limit is adopted, found by its name when the run has no record of it.
// Any other idle or done has its tab closed (see closeEarlier). Any other is renamed so the new
// worker can have the name; its tab stays as it is. One Herdr can't tell the state of is read again
// for a while, and then counts as still at work. It returns the earlier worker's state as last read,
// and whether the ticket's run is over, with the reason the run must stop, if any.
func (r *ticketRun) takeUpEarlier(ctx context.Context, earlier workerSpot, asked bool) (AgentState, bool,
	*stopReason) {
	o, id := r.o, r.id
	st, err := o.earlierKnownState(ctx, id, earlier)
	adopted := time.Now() // an adopted worker's hooks reported anything older in its earlier turns
	if ctx.Err() != nil {
		return st, true, errInterrupted
	}
	if err != nil {
		return st, true, halt(ExitTool, stopTerminalFailed,
			": cannot tell whether an earlier worker for %s is still in its tab: %v", id, err).causedBy(err)
	}
	switch earlierStep(st, asked) {
	case adoptEarlier:
		if earlier.wt == "" { // one the run has no record of, as one an earlier run left at the limit
			if earlier = r.foundEarlier(ctx); ctx.Err() != nil {
				return st, true, errInterrupted
			}
			if earlier.wt == "" {
				return st, true, halt(ExitTool, stopAgentBusy,
					": an earlier worker for %s is still %s, its worktree unknown; stopping rather than starting a "+
						"second one on %s", id, stillIn(st, "its tab"), r.br)
			}
		}
		o.info("  %s's earlier worker is still %s; adopting it rather than starting another",
			id, stillIn(st, "tab "+earlier.tab))
		return st, true, r.adopt(ctx, earlier, st, adopted)
	case stopForEarlier:
		return st, true, halt(ExitTool, stopAgentBusy,
			": an earlier worker for %s is still %s; stopping rather than starting a second one on %s",
			id, stillIn(st, "its tab"), r.br)
	case tellEarlier:
		return r.tellEarlier(ctx, earlier, st, adopted)
	case closeEarlier:
		stop := r.closeEarlier(ctx, earlier, st)
		return st, stop != nil, stop
	case renameEarlier:
		stop := r.renameEarlier(ctx)
		return st, stop != nil, stop
	}
	return st, false, nil
}

// tellEarlier tells the earlier worker of a ticket back from its answered question, idle in its
// tab (st), to carry on, and adopts it if it does, as of adopted. One its hooks show waiting on a
// permission prompt is adopted without being told: it carries on already. It returns as takeUpEarlier
// does.
func (r *ticketRun) tellEarlier(ctx context.Context, earlier workerSpot, st AgentState, adopted time.Time) (
	AgentState, bool, *stopReason) {
	o, id := r.o, r.id
	if at, asks := o.permissionPending(earlier, st); asks {
		o.info("  %s's earlier worker in tab %s waits on a permission prompt; adopting it rather than telling it %s",
			id, earlier.tab, earlier.told())
		return st, true, r.adopt(ctx, earlier, st, at)
	}
	// Told to carry on, it may take that up however resume ends, Ctrl+C included: it carries the
	// ticket from now on, as one adopted does.
	o.takeOn(r.t, earlier)
	if o.tellToCarryOn(ctx, id, r.agent, earlier) {
		o.info("  %s's earlier worker in tab %s was told %s and carries on; adopting it",
			id, earlier.tab, earlier.told())
		return StateWorking, true, r.adopt(ctx, earlier, StateWorking, adopted)
	}
	if ctx.Err() != nil {
		return StateIdle, true, errInterrupted
	}
	// Not seen to take it up, it may have all the same, as when Herdr lost sight of it or it
	// started just after Enter was pressed: one at work now is adopted, as its branch must not
	// be rebased under it; only one still idle, or gone, is replaced.
	st, err := o.earlierKnownState(ctx, id, earlier)
	if ctx.Err() != nil {
		return st, true, errInterrupted
	}
	if err != nil {
		return st, true, halt(ExitTool, stopTerminalFailed,
			": cannot tell whether %s's earlier worker took up being told %s: %v", id, earlier.told(), err).causedBy(err)
	}
	switch st {
	case StateWorking, StateBlocked, StateUnknown, StateLimited:
		o.info("  %s's earlier worker in tab %s is %s after being told %s; adopting it rather than starting another",
			id, earlier.tab, st, earlier.told())
		return st, true, r.adopt(ctx, earlier, st, adopted)
	case StateGone:
		o.info("  %s's earlier worker in tab %s has gone; starting a new one", id, earlier.tab)
		if ctx.Err() != nil {
			return st, true, errInterrupted
		}
		return st, false, nil // no name to free
	}
	o.info("  %s's earlier worker in tab %s is still %s; starting a new one", id, earlier.tab, st)
	stop := r.renameEarlier(ctx)
	return st, stop != nil, stop
}

// closeEarlier closes the tab of the ticket's earlier worker, earlier, left idle or done (st) there
// when its ticket was set aside: the new worker takes up its worktree, as each ticket has one, so the
// tab holds nothing to review but its session, whose transcript the log names. One whose tab the run
// doesn't know, as one an earlier run left, is looked for under the ticket's name (see foundEarlier).
// One whose tab the terminal can't say either, or that stays open (see closeOwnTab), is renamed
// instead. It returns as renameEarlier does.
func (r *ticketRun) closeEarlier(ctx context.Context, earlier workerSpot, st AgentState) *stopReason {
	o, id := r.o, r.id
	if ctx.Err() != nil {
		return errInterrupted
	}
	if earlier.tab == "" {
		earlier = r.foundEarlier(ctx)
	}
	if ctx.Err() != nil {
		return errInterrupted
	}
	if earlier.tab == "" {
		return r.renameEarlier(ctx)
	}
	transcript := "its session's transcript isn't known"
	if s, ok := o.sessionOf(earlier.wt, earlier.hooks); ok && s.Transcript != "" {
		transcript = "its session's transcript is " + s.Transcript
	}
	said, closed := o.closeOwnTab(ctx, id, earlier.tab)
	if !closed {
		o.info("  %s's earlier worker is %s in tab %s; %s", id, st, earlier.tab, said)
		return r.renameEarlier(ctx)
	}
	o.info("  %s's earlier worker was %s in tab %s; its tab is closed, as the new worker takes up its worktree; %s",
		id, st, earlier.tab, transcript)
	return nil
}

// foundEarlier is where the ticket's earlier worker is when the run has no record of it, as one an
// earlier run left in its tab as it set the ticket aside: the tab the terminal has it in under the
// ticket's name, and the ticket's worktree, whose hooks' records are the last worker's there. Its tab
// is "" when the terminal can't say.
func (r *ticketRun) foundEarlier(ctx context.Context) workerSpot {
	o := r.o
	tab, err := o.namer.AgentTab(ctx, r.agent)
	if err != nil {
		if ctx.Err() == nil {
			o.log.Raw("", fmt.Errorf("cannot find the tab of %s's earlier worker: %w", r.id, err))
		}
		return workerSpot{}
	}
	if tab == "" {
		return workerSpot{}
	}
	wt, _ := o.worktrees.WorktreeOf(ctx, o.cfg.Repo, r.br) // "": its transcript isn't known
	return workerSpot{tab: tab, wt: wt, hooks: wt != ""}
}

// renameEarlier renames the earlier worker still in its tab under the ticket's name, for the new
// worker to have the name. It returns the reason the run must stop when it can't.
func (r *ticketRun) renameEarlier(ctx context.Context) *stopReason {
	o := r.o
	if ctx.Err() != nil {
		return errInterrupted
	}
	name := o.namer.FreeName(ctx, r.agent)
	if name == "" || o.namer.RenameAgent(ctx, r.agent, name) != nil {
		if ctx.Err() != nil {
			return errInterrupted
		}
		return halt(ExitTool, stopAgentNameTaken, ": an earlier worker for %s holds its name and could not be renamed", r.id)
	}
	o.info("  earlier worker for %s renamed to %s; its tab is left open", r.id, name)
	return nil
}

// prepare makes the ticket's worktree: one per ticket, and a ticket that comes back (deferral ended)
// resumes its old branch. It reports whether the ticket's run is over, with the reason the run must
// stop, if any: a branch that conflicts with Base sets the ticket aside.
func (r *ticketRun) prepare() (preparedWorktree, bool, *stopReason) {
	o, c, id, br := r.o, r.o.cfg, r.id, r.br
	p, conflicts, s := o.prepareWorktree(r.keep, id, br)
	if s != nil {
		return p, true, s
	}
	wt := p.wt
	o.footprintWorktree(id, wt)
	if !conflicts {
		return p, false, nil
	}
	// A worker is told not to rebase, so it would work on a stale base and its merge would end
	// in MERGE_CONFLICT anyway: set the ticket aside until its branch is rebased by hand.
	o.putAside(r.keep, r.t, asideTexts{
		note: fmt.Sprintf("Orchestra: %s conflicts with %s, so no worker was started on it. "+
			"Rebase it by hand (cd %s && git rebase %s, resolve, git rebase --continue), "+
			"then bring it back with: bd undefer %s",
			br, c.Base, command.ShellWord(wt), c.Base, id),
		reason: fmt.Sprintf("%s conflicts with %s; rebase it in %s", br, c.Base, wt),
		detail: "its branch conflicts with " + c.Base,
		deferred: fmt.Sprintf("  REBASE_FAILED: %s conflicts with %s -> %s deferred without starting a worker; "+
			"rebase it in %s, then bd undefer %s",
			br, c.Base, id, wt, id),
		failed: fmt.Sprintf("%s conflicts with %s, and bd could not defer %s", br, c.Base, id),
		then:   "rebase it in " + wt,
	})
	return p, true, nil
}

// start starts a new worker on the ticket in worktree wt, and once it has begun on its prompt,
// concludes it. It returns as work does.
func (r *ticketRun) start(ctx context.Context, wt string) *stopReason {
	o, c, id := r.o, r.o.cfg, r.id
	ws, done, stop := r.setup(ctx, wt, false)
	if done {
		return stop
	}
	prompt := ws.prompt

	head, err := o.checkout.Head(ctx, c.Repo, r.br) // a worker that commits moves it; "" on failure
	o.log.Raw("", err)

	launched, stop := o.startWorker(ctx, r.t, r.agent, wt, ws.args, ws.launch, "")
	if stop != nil {
		return stop
	}
	if !launched.rules {
		prompt = ws.full // started without its rules, as Herdr refused them: they are pasted with the rest
	}
	defer o.status(Status{Ticket: id, Gone: true})
	tab, started := launched.tab, launched.started

	w := o.newWatcher(wt, Status{Ticket: id, Title: r.t.Title, Tab: tab, Started: started})
	stopWatch := o.watch(ctx, w)
	defer stopWatch()

	if !o.promptTaken(ctx, id, r.agent, prompt, launched.atLaunch, false) {
		if ctx.Err() != nil {
			return errInterrupted
		}
		o.putAside(r.keep, r.t, asideTexts{
			note: fmt.Sprintf("Orchestra: the worker in tab %s never started on its prompt; "+
				"deferred so it can be retried (worktree %s).", tab, wt),
			reason: "the worker never started on its prompt",
			detail: "its worker never started on the prompt",
			deferred: fmt.Sprintf("  PROMPT_FAILED: %s's worker never started on its prompt -> deferred; "+
				"worktree %s and tab %s left open", id, wt, tab),
			failed: id + "'s worker never started on its prompt, and bd could not defer it",
			then:   fmt.Sprintf("worktree %s and tab %s left open", wt, tab),
		})
		o.leaveOpen(r.keep, worker{id: id, br: r.br, wt: wt, tab: tab, agent: r.agent, pane: launched.pane,
			started: started, hooks: launched.hooks})
		return nil
	}

	stopWatch() // the settle loop reports from here on
	// It has begun on its prompt now, and reports through hooks only if it was started with them.
	return r.conclude(ctx, worker{id: id, br: r.br, wt: wt, tab: tab, agent: r.agent, pane: launched.pane,
		started: started, hooks: launched.hooks}, head, w.report)
}

// workerSetup is what a worker is started with: its arguments and, for a new worker (not resumed),
// its first prompt.
type workerSetup struct {
	args   workerArgs
	launch string // a new worker's prompt file, to start it from; "" to paste its prompt
	prompt string // a new worker's first prompt: without its standing rules when it has them
	full   string // a new worker's whole prompt, pasted if it starts without its rules
}

// setup prepares the files the ticket's worker, new or resumed, starts with in worktree wt (see
// workerFiles). It reports whether the worker is not to start, and why the run must stop then, if
// it must: a run file leading out of the worktree sets the ticket aside (see setAsideEscaped), and
// the MCP servers failing otherwise stop the run.
func (r *ticketRun) setup(ctx context.Context, wt string, resumed bool) (workerSetup, bool, *stopReason) {
	ws, err := r.o.workerFiles(ctx, r.t, wt, resumed, r.cut)
	if e := escapeOf(err); e != nil {
		return ws, true, r.o.setAsideEscaped(r.keep, r.t, wt, e)
	}
	if err != nil {
		return ws, true, halt(ExitTool, stopStartFailed, " for %s: %v", r.id, err).causedBy(err)
	}
	return ws, false, nil
}

// workerFiles prepares ticket t's worker's files in worktree wt: its MCP servers, its standing
// rules, a new worker's prompt file and a Claude worker's hooks. It returns the first error that
// stops the worker from starting: an escape (see escapeOf), or the MCP servers failing. The others
// the worker does without are logged, and left out. cut says its branch was cut from Base for it, so
// a new worker is told of no earlier attempt without asking git (see earlierNote).
func (o *Loop) workerFiles(ctx context.Context, t Ticket, wt string, resumed, cut bool) (workerSetup, error) {
	c, id := o.cfg, t.ID
	var ws workerSetup
	// A Claude worker gets the project's MCP servers and no others. Without them it would start with
	// every server on the machine, so a file it can't have stops the run rather than the worker.
	mcp, err := o.mcpArgs(wt)
	if err != nil {
		return ws, err
	}
	ws.args.mcp = mcp

	// A Claude worker's standing rules go in its system prompt, which survives compaction (see
	// rules.go). Claude Code keeps no system prompt with a session: a resumed worker is given them again.
	without := id + "'s worker gets its whole prompt as its first message"
	if resumed {
		without = id + "'s resumed worker goes without its standing rules in its system prompt"
	}
	rules, err := o.rulesArgs(wt, id)
	if ws.args.rules, err = optional(o.log, rules, err, without); err != nil {
		return ws, err
	}

	if !resumed {
		earlier := ""
		if !cut {
			earlier = o.earlierNote(ctx, branchOf(id), wt)
		}
		first := firstNotes(o.withLinks(ctx, t), earlier)
		ws.full = o.fullPrompt(id, first)
		ws.prompt = ws.full
		if ws.args.rules != nil {
			ws.prompt = ticketPrompt(id, first)
		}
		// Claude starts with its prompt already submitted, so nothing is pasted into its input box.
		// Herdr can only pass a one-line argument, so the prompt goes in a file the worker reads.
		if c.LaunchPrompt && c.Agent().LaunchPrompt {
			launch, err := project.WriteLaunchPrompt(wt, id, ws.prompt)
			ws.launch, err = optional(o.log, launch, err, "cannot write the launch prompt for "+id+", pasting it instead")
			if err != nil {
				return ws, err
			}
		}
	}

	// A Claude worker reports each tool it uses, through hooks loaded for it alone, whose settings give
	// it its share of the machine's cores. A resumed one keeps the files the worker before it edited in
	// the footprint (sessionOf found a reporter).
	var report []string
	switch {
	case resumed:
		o.newWorkerHooks(id, wt)
		report, err = o.envReporter(o.workerEnv(ctx)).ResumeArgs(wt)
		ws.args.report, err = optional(o.log, report, err, "cannot set up "+id+"'s resumed worker to report what it does")
	case o.reporter != nil && c.Agent().Hooks:
		o.newWorkerHooks(id, wt)
		report, err = o.envReporter(o.workerEnv(ctx)).ReportArgs(wt)
		ws.args.report, err = optional(o.log, report, err, "cannot set up "+id+"'s worker to report what it does")
	}
	return ws, err
}

// withLinks is ticket t with the tickets it depends on as bd show gives them, with their close reasons,
// for a new worker's first message (see handoffNote): bd ready, which t was picked from, gives none. A
// show that fails is logged, and t's worker starts without its blockers' hand-offs.
func (o *Loop) withLinks(ctx context.Context, t Ticket) Ticket {
	if t.Dependencies != nil {
		return t
	}
	shown, err := o.tickets.Show(ctx, t.ID)
	if err != nil {
		if ctx.Err() == nil { // else bd failed for the stop, which the run's INTERRUPTED line says
			o.log.Raw("", fmt.Errorf("cannot read what the tickets %s builds on handed on, starting without it: %w",
				t.ID, err))
		}
		return t
	}
	t.Dependencies = shown.Dependencies
	return t
}

// optional is v, which a worker can start without, or, when err says it couldn't be had, its zero
// value, the failure logged with what the worker does without it (what). An escape (see escapeOf)
// it returns instead: it sets the ticket aside.
func optional[T any](log *Log, v T, err error, what string) (T, error) {
	var zero T
	if err == nil {
		return v, nil
	}
	if escapeOf(err) != nil {
		return zero, err
	}
	log.Raw("", fmt.Errorf("%s: %w", what, err))
	return zero, nil
}

// adopt takes on the worker an asked ticket left in its tab, which carries on with the ticket now
// its question is answered, as if it had just been started on it: it waits for it to settle and
// merges or sets aside its work as usual. st is its status, as just read, at adopted.
func (r *ticketRun) adopt(ctx context.Context, w workerSpot, st AgentState, adopted time.Time) *stopReason {
	o, id := r.o, r.id
	base := o.takeOn(r.t, w)
	o.footprintWorktree(id, w.wt)
	head, err := o.checkout.Head(ctx, o.cfg.Repo, r.br) // "" on failure: its work is not taken for none
	o.log.Raw("", err)
	o.updateActive(id, func(s *Status) { s.Agent = st })
	defer o.status(Status{Ticket: id, Gone: true})
	// Its hooks' record may still end with the Stop of the turn it asked in, which would pass for the
	// end of this one: only what they reported once it was adopted counts.
	return r.conclude(ctx, worker{id: id, br: r.br, wt: w.wt, tab: w.tab, agent: r.agent, pane: w.pane,
		started: base.Started, hooks: w.hooks, since: adopted}, head, o.newWatcher(w.wt, base).report)
}

// takeOn makes the worker an asked ticket t left in its tab, w, the ticket's again, as it is told to
// carry on or adopted: placed, and active from now on, and no longer asked. A stop or Ctrl+C from
// then on leaves the ticket running, labelled (see leaveRunning) and carried over to the next run
// (see saveCarried), as its worker may close it once orchestra has gone. It returns the ticket's
// Status.
func (o *Loop) takeOn(t Ticket, w workerSpot) Status {
	id := t.ID
	o.place(id, workerSpot{tab: w.tab, pane: w.pane, wt: w.wt, fork: w.fork, hooks: w.hooks})
	st := Status{Ticket: id, Title: t.Title, Tab: w.tab, Started: time.Now()}
	o.setActive(st)
	o.book.answer(id) // back from a question: set aside again once it settles, it stays out
	return st
}

// adoptAsked takes on the worker an asked ticket left in its tab, which claimed the ticket again or
// closed it there without it coming back through bd ready (see followAsked), as work takes on one
// that is still working when its ticket comes back. One idle with its ticket in progress is told
// the answer is in; one that closed it settles at once and its work is merged; one gone from its tab
// has its session resumed. It returns as work does.
func (r *ticketRun) adoptAsked(ctx context.Context, w workerSpot, resume *Session) *stopReason {
	o, t, id := r.o, r.t, r.id
	// Its worker's from now on, so a stop or Ctrl+C before it settles leaves it labelled and carried
	// over: it is no longer asked.
	o.takeOn(t, w)
	if HasLabel(t, UnmergedLabel) {
		o.setLabelled(id, true) // reopened after an earlier run left it unmerged: merging removes the label
	}
	if resume != nil {
		return r.resumeSession(ctx, w.wt, w.tab, w.question, goneMidTicket, *resume)
	}
	st, err := o.readStatus(ctx, r.agent, 5)
	adopted := time.Now() // its hooks reported anything older in its earlier turns
	if ctx.Err() != nil {
		return errInterrupted
	}
	if err != nil {
		return halt(ExitTool, stopTerminalFailed,
			": cannot tell what %s's worker is doing in tab %s: %v", id, w.tab, err).causedBy(err)
	}
	at, asks := o.permissionPending(w, st)
	switch {
	case asks:
		adopted = at
		o.info("  %s's worker in tab %s waits on a permission prompt with the ticket %s; adopting it rather than "+
			"telling it %s", id, w.tab, t.Status, w.told())
	case t.Status != StatusClosed && (st == StateIdle || st == StateDone) && o.tellToCarryOn(ctx, id, r.agent, w):
		o.info("  %s's worker in tab %s was told %s and carries on; adopting it", id, w.tab, w.told())
		st = StateWorking
	case ctx.Err() != nil:
		return errInterrupted
	default:
		o.info("  %s's worker in tab %s is %s with the ticket %s; adopting it", id, w.tab, st, t.Status)
	}
	return r.adopt(ctx, w, st, adopted)
}

// budgetNote tells a worker the time its ticket has when the run sets a ticket limit, as agents pace
// themselves to a stated budget; "" without one.
func (o *Loop) budgetNote() string {
	limit := o.cfg.TicketLimit
	if limit <= 0 {
		return ""
	}
	after := "stops the run"
	if o.cfg.Overnight {
		after = "has its ticket set aside"
	}
	return fmt.Sprintf("\n\nYou have about %s for this ticket: a worker still going after that %s, "+
		"so pace yourself to it.\n", command.ShortDuration(limit), after)
}

// permissionPending says whether the worker a ticket left in its tab, w, which the terminal shows in
// state st, waits on a permission prompt, as its hooks tell, and when it asked. Herdr may show such a
// worker idle or done: telling it to carry on would press Enter on the prompt's dialog and so allow
// the tool, which is the maintainer's to answer. Adopted as of then, it is waited on as a blocked
// worker is (see waitSettled), as the request counts among what its hooks reported once adopted.
func (o *Loop) permissionPending(w workerSpot, st AgentState) (time.Time, bool) {
	h := o.hooksOf(worker{wt: w.wt, hooks: w.hooks})
	if !asksPermission(h, st) {
		return time.Time{}, false
	}
	return h.use.At, true
}

// tellToCarryOn tells an asked ticket's earlier worker, idle in its tab, that its question is answered and
// to carry on, so it keeps what it already knows of the ticket; one the last run left running, with
// no question, is told its ticket is back. It reports whether it took that up.
func (o *Loop) tellToCarryOn(ctx context.Context, id, agent string, w workerSpot) bool {
	msg := fmt.Sprintf("Orchestra: your question %s is answered. Read the answer with bd show %s, "+
		"claim %s again with bd update %s --claim, and carry on with it where you left off, as your instructions say.",
		w.question, w.question, id, id)
	if w.question == "" {
		msg = fmt.Sprintf("Orchestra: %s is yours again. Claim it with bd update %s --claim, "+
			"and carry on with it where you left off, as your instructions say.", id, id)
	}
	if o.deliverPrompt(ctx, agent, msg) {
		return true
	}
	if ctx.Err() == nil {
		o.log.Raw("", fmt.Errorf("%s's earlier worker in tab %s did not take up being told %s",
			id, w.tab, w.told()))
	}
	return false
}

// told is what resume tells the worker: "Q is answered", or, with no question, "to carry on".
func (w workerSpot) told() string {
	if w.question == "" {
		return "to carry on"
	}
	return w.question + " is answered"
}

// worker is a ticket's worker as the run waits on it and merges its work: the ticket, its branch
// and worktree, the Herdr tab and pane it runs in and its name there, and how to read its hooks.
type worker struct {
	id, br, wt, tab, agent string
	pane                   string    // its pane, looked in should it lose its name (see nameLost); "" if not known
	started                time.Time // when it was started on the ticket, or adopted
	hooks                  bool      // it reports what it does through hooks
	since                  time.Time // its hooks' reports count from then on (zero: any report counts)
}

// conclude waits for the ticket's worker, w, to settle, and then does what its ticket's status says:
// merge it, set it aside, or stop the run. A ticket set aside with its worker left in its tab is
// followed there, should the worker close it later (see leaveOpen). In an overnight run, a stop that
// needs the maintainer for this ticket alone sets it aside instead (see asideOvernight). head is its
// branch's commit before the worker began. It sets r.how to how the worker settled.
func (r *ticketRun) conclude(ctx context.Context, w worker, head string,
	report func(ctx context.Context, st AgentState, err error)) *stopReason {
	o, t, keep := r.o, r.t, r.keep
	id, br, wt, tab := w.id, w.br, w.wt, w.tab
	idleAt, stop := o.waitSettled(ctx, w, time.Now(), report)
	if stop != nil {
		return r.asideOvernight(ctx, w, stop)
	}

	// Beads, not the worker's own report, decides what happened. A ticket blocked on an open
	// question is out of the queue until the maintainer answers; the run goes on without it. The
	// worker has settled: what follows is bookkeeping, on keep, but for the waits merging has (a
	// check, a worker resolving conflicts) and evidence for triage, which Ctrl+C skips. bd failing
	// once, as when another bd holds the database, doesn't stop the run: the ticket is read again.
	info, showErr := o.showTries(keep, id)
	r.how = settledSlow
	if q := OpenQuestion(info); q != nil && info.Status != StatusClosed {
		o.book.ask(id, workerSpot{tab: tab, pane: w.pane, wt: wt, question: q.ID, title: q.Title, hooks: w.hooks})
		o.reopen(keep, id, info.Status, "come back once "+q.ID+" is answered") // back in the queue once answered
		o.emit(Event{Kind: EvAsked, Ticket: id, Title: t.Title, Detail: q.ID + ": " + q.Title, Text: fmt.Sprintf(
			"  ASKED: %s waits on your answer to %s (%s); answer with: the dashboard's a key, orchestra answer %s "+
				"or bd human respond %s; worktree %s and tab %s left open",
			id, q.ID, q.Title, q.ID, q.ID, wt, tab)})
		return nil
	}
	// A worker that found its ticket needs the code of a ticket not merged yet links that ticket as
	// a blocker and stops, with nothing done: its ticket started too early, and goes back in the
	// queue rather than be set aside. With work of its own, or a blocker merged already, it is set
	// aside for review as below.
	if s := info.Status; s == StatusOpen || s == StatusInProgress {
		if on := o.unmergedBlockers(keep, info); len(on) > 0 && o.nothingDone(keep, w) {
			r.how = requeued // neither failed at once nor done: the hold for the environment counts it for nothing
			o.waitForMerge(keep, t, w, s, on)
			return nil
		}
	}
	switch s := info.Status; outcomeOf(s) {
	case outcomeClosed:
		if s := o.finish(ctx, w); s != nil {
			return s
		}
	case outcomeDeferred:
		o.book.setAside(id)
		empty := o.nothingDone(keep, w)
		left := fmt.Sprintf("worktree %s and tab %s left open", wt, tab)
		if empty {
			left = nothingLeft
		}
		o.emit(Event{Kind: EvDeferred, Ticket: id, Title: t.Title, Detail: "by the worker", Text: fmt.Sprintf(
			"  %s deferred by worker; %s", id, left)})
		r.leaveAside(ctx, w, empty, "the worker deferred it")
	case outcomePaused:
		if o.workerGone(keep, w) {
			return r.pausedGone(ctx, w)
		}
		// Most likely waiting for an answer: stop rather than start the next ticket around it.
		o.appendNotes(keep, id, fmt.Sprintf(
			"Orchestra: worker in tab %s went idle with the ticket still in_progress (worktree %s).", tab, wt))
		return r.asideOvernight(ctx, w, halt(ExitStuck, stopPaused,
			": %s still in_progress in tab %s (worktree %s); stopping so it can be answered", id, tab, wt).
			setsAside("its worker went idle with the ticket still in_progress"))
	case outcomeUnreadable:
		return halt(ExitTool, stopStatusUnreadable,
			" for %s%s; stopping rather than guessing (worktree %s and tab %s left open)",
			id, because(showErr), wt, tab).causedBy(showErr)
	case outcomeUnfinished:
		if o.failedAtOnce(keep, s, w.started, idleAt, br, head, wt) {
			r.how = settledFast
		}
		a := asideTexts{
			note: fmt.Sprintf("Orchestra: worker in tab %s settled with the ticket still '%s'; "+
				"deferred for review (worktree %s).", tab, s, wt),
			reason:   fmt.Sprintf("worker finished without closing; see tab %s and worktree %s", tab, wt),
			detail:   "still " + string(s) + ", noted for review",
			deferred: fmt.Sprintf("  %s still %s -> noted and deferred; worktree %s and tab %s left open", id, s, wt, tab),
			failed:   fmt.Sprintf("%s still %s, and bd could not defer it", id, s),
			then:     fmt.Sprintf("worktree %s and tab %s left for review", wt, tab),
		}
		empty := o.nothingDone(keep, w)
		if empty {
			a.note = fmt.Sprintf("Orchestra: worker in tab %s settled with the ticket still '%s' and nothing done "+
				"(no commits, no changes); deferred for review, its worktree, branch and tab removed.", tab, s)
			a.reason = "worker finished without closing, and with nothing done"
			a.deferred = fmt.Sprintf("  %s still %s -> noted and deferred; %s", id, s, nothingLeft)
			a.then = nothingLeft
		}
		settled := ""
		if o.putAside(keep, t, a) {
			settled = fmt.Sprintf("the worker settled with the ticket still '%s', so the orchestrator deferred it", s)
		}
		r.leaveAside(ctx, w, empty, settled)
	}
	return nil
}

// leaveAside leaves the ticket of w's worker, just set aside, for review: the worker, if it left
// anything done, is followed in its tab (see leaveOpen); triage is told why it was deferred, unless why
// is "" (bd could not defer it); and a worker that left nothing done (empty) has its worktree, branch
// and tab removed, once triage has its evidence.
func (r *ticketRun) leaveAside(ctx context.Context, w worker, empty bool, why string) {
	o, keep := r.o, r.keep
	if !empty {
		o.leaveOpen(keep, w)
	}
	if why != "" {
		o.triageDeferred(ctx, w.id, why, w.wt)
	}
	if empty {
		o.dropNothingDone(keep, w)
	}
}

// nothingLeft is what a set-aside's line says of a worker that left nothing done (see nothingDone).
const nothingLeft = "nothing done, so its worktree, branch and tab are removed"

// dropNothingDone removes the worktree, branch and tab of w's worker, set aside with nothing done (see
// nothingDone): nothing there is left to review. Its triage has its evidence by then. A failure is
// warned of, as the set-aside's line said they were removed.
func (o *Loop) dropNothingDone(ctx context.Context, w worker) {
	o.repoMu.Lock()
	cleaned := o.removeWorktree(ctx, w.id, w.wt, w.br)
	o.repoMu.Unlock()
	if !cleaned {
		o.emit(Event{Kind: EvWarn, Ticket: w.id, Text: fmt.Sprintf(
			"  CLEANUP_FAILED for %s / %s (git output is in %s); tab %s left open", w.wt, w.br, o.cfg.LogPath, w.tab)})
		return
	}
	if said, closed := o.closeOwnTab(ctx, w.id, w.tab); !closed {
		o.info("  %s's worktree and branch removed; %s", w.id, said)
	}
}

// nothingDone reports whether w's worker left nothing of its own: its branch has no commits beyond
// Base and its worktree is clean; not where git can't tell.
func (o *Loop) nothingDone(ctx context.Context, w worker) bool {
	c := o.cfg
	if n, err := o.merger.CountCommits(ctx, c.Repo, c.Base+".."+w.br); err != nil || n != 0 {
		o.log.Raw("", err)
		return false
	}
	dirty, err := o.checkout.DirtyWorktree(ctx, w.wt)
	o.log.Raw("", err)
	return err == nil && dirty == ""
}

// waitForMerge puts ticket t back in the queue, open, once its worker, w, has stopped with it still
// status (open or in progress) and nothing done, having found it blocked by tickets not merged yet,
// on. Its worktree, branch and tab hold nothing, so they are removed. It is neither set aside, even
// if it asked a question earlier in the run, nor triaged: the holds start it again once they merge,
// from a Base with their code (see holds.why).
func (o *Loop) waitForMerge(ctx context.Context, t Ticket, w worker, status TicketStatus, on []string) {
	c := o.cfg
	id, br, wt, tab := w.id, w.br, w.wt, w.tab
	blockers, does := strings.Join(on, ", "), "it does"
	if len(on) > 1 {
		does = "they do"
	}
	o.reopen(ctx, id, status, "be back in the queue as it waits for "+blockers)
	// Back in the queue, it is no longer set aside: not as a ticket that asked earlier in the run.
	o.requeue(id, "it waits for "+blockers+" to merge")
	o.appendNotes(ctx, id, fmt.Sprintf("Orchestra: started before %s merged; back in the queue until %s.", blockers, does))
	o.repoMu.Lock()
	cleaned := o.removeWorktree(ctx, id, wt, br)
	o.repoMu.Unlock()
	left := fmt.Sprintf("CLEANUP_FAILED for %s / %s (git output is in %s); tab %s left open", wt, br, c.LogPath, tab)
	if cleaned {
		left = o.closeWorkerTab(ctx, id, tab)
	}
	o.emit(Event{Kind: EvWaits, Ticket: id, Title: t.Title, Detail: blockers, Text: fmt.Sprintf(
		"  WAITS_FOR_MERGE: %s waits for %s to merge; its worker stopped with nothing done, "+
			"so it is back in the queue until %s; %s", id, blockers, does, left)})
}

// outcome starts at one so an unset outcome is no outcome, never "closed".
type outcome int

const (
	outcomeClosed outcome = iota + 1
	outcomeDeferred
	outcomePaused
	outcomeUnreadable
	outcomeUnfinished // any other status: note it and defer for review
)

func outcomeOf(status TicketStatus) outcome {
	switch status {
	case StatusClosed:
		return outcomeClosed
	case StatusDeferred:
		return outcomeDeferred
	case StatusInProgress:
		return outcomePaused
	case StatusUnknown:
		return outcomeUnreadable
	}
	return outcomeUnfinished
}

// closeTab closes a worker's tab; a failure is only logged, as the tab is then merely left open.
func (o *Loop) closeTab(ctx context.Context, tab string) {
	if err := o.tabs.CloseTab(ctx, tab); err != nil {
		o.log.Raw("", fmt.Errorf("cannot close tab %s: %w", tab, err))
	}
}

// escapeOf is the *project.EscapeError in err's chain, or nil.
func escapeOf(err error) *project.EscapeError {
	if e, ok := errors.AsType[*project.EscapeError](err); ok {
		return e
	}
	return nil
}

// setAsideEscaped sets ticket t aside without starting a worker: part of the path to its run files
// in worktree wt is a symlink orchestra won't follow (see project.OpenRun), because it leads out of
// the worktree or stands in place of .orchestra or .orchestra/run. The worker's own files are its to
// change, so only the user can say whether the link is safe to remove.
func (o *Loop) setAsideEscaped(ctx context.Context, t Ticket, wt string, e *project.EscapeError) *stopReason {
	id := t.ID
	o.log.Raw("", fmt.Errorf("%s: %w", id, e))
	why := fmt.Sprintf("its worktree's %s points outside the worktree", e.Path)
	if errors.Is(e, project.ErrRunLink) {
		why = fmt.Sprintf("its worktree's %s is a symlink", e.Path)
	}
	link := filepath.Join(wt, e.Path)
	o.putAside(ctx, t, asideTexts{
		note: fmt.Sprintf("Orchestra: %s (%s), so no worker was started on it: "+
			"orchestra writes a worker's run files only in its worktree's own .orchestra/run folder, which git ignores. "+
			"Look at what it points to, remove the link (rm %s), then bring it back with: bd undefer %s",
			why, link, command.ShellWord(link), id),
		reason: why,
		detail: why,
		deferred: fmt.Sprintf("  RUN_FILES_OUTSIDE: %s -> %s deferred without starting a worker; "+
			"remove the link %s, then bd undefer %s", why, id, link, id),
		failed: fmt.Sprintf("%s, and bd could not defer %s", why, id),
		then:   "remove the link in " + wt,
	})
	return nil
}
