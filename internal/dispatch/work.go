package dispatch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// work runs one ticket from worktree to merge. It returns a reason when the run must stop; the
// ticket then stays listed as active (still being worked on). It sets how to how the worker
// settled, for the hold for the environment. A panic in it is such a reason: PANIC.
func (o *Loop) work(ctx context.Context, t Ticket, how *settling) (stop *stopReason) {
	c := o.cfg
	id, br := t.ID, "wt/"+t.ID
	defer func() {
		if p := recover(); p != nil {
			stop = o.panicStop(id, p)
		} else if stop == nil {
			o.clearActive(id)
		}
	}()
	earlier, asked := o.asked(id)
	o.setAsked(id, nil) // back from a question: set aside again, it stays out
	if HasLabel(t, UnmergedLabel) {
		o.setLabelled(id, true) // reopened after an earlier run left it unmerged: merging removes the label
	}

	if ctx.Err() != nil {
		return errInterrupted // Ctrl+C came as it was dispatched
	}
	// What must finish once begun (making the worktree, merging, the notes on what happened) runs on
	// keep, which Ctrl+C doesn't cancel, so the repository and the ticket aren't left half done.
	// Each command still has its time limit.
	keep := context.WithoutCancel(ctx)

	// The worker's Herdr name: Herdr takes fewer characters than a ticket ID can hold.
	agent := o.agentName(id)

	// A returning ticket's earlier worker may still sit in its tab under the ticket's name, which
	// Herdr keeps unique. It is looked at before the worktree is touched: one still at work there
	// must not have its branch rebased under it. One left by a question asked in this run may have
	// had its answer in its tab and carried on: it is adopted, or, idle, told the answer is in. Any
	// other is renamed so the new worker can have the name; its tab stays as it is.
	st, err := o.readStatus(ctx, agent, 5)
	adopted := time.Now() // an adopted worker's hooks reported anything older in its earlier turns
	if ctx.Err() != nil {
		return errInterrupted
	}
	if err != nil {
		return halt(ExitTool, stopHerdrFailed,
			": cannot tell whether an earlier worker for %s is still in its tab: %v", id, err).causedBy(err)
	}
	switch st {
	case StateGone:
	case StateWorking, StateBlocked:
		if asked {
			o.info("  %s's earlier worker is still %s in tab %s; adopting it rather than starting another",
				id, st, earlier.tab)
			return o.adopt(ctx, t, agent, earlier, st, adopted, how)
		}
		return halt(ExitTool, stopAgentBusy,
			": an earlier worker for %s is still %s in its tab; stopping rather than starting a second one on %s",
			id, st, br)
	default:
		if asked && (st == StateIdle || st == StateDone) && o.resume(ctx, id, agent, earlier) {
			o.info("  %s's earlier worker in tab %s was told %s is answered and carries on; adopting it",
				id, earlier.tab, earlier.question)
			return o.adopt(ctx, t, agent, earlier, StateWorking, adopted, how)
		}
		if ctx.Err() != nil {
			return errInterrupted
		}
		name := o.namer.FreeName(ctx, agent)
		if name == "" || o.namer.RenameAgent(ctx, agent, name) != nil {
			if ctx.Err() != nil {
				return errInterrupted
			}
			return halt(ExitTool, stopAgentNameTaken, ": an earlier worker for %s holds its name and could not be renamed", id)
		}
		o.info("  earlier worker for %s renamed to %s; its tab is left open", id, name)
	}

	// One worktree per ticket. A ticket that comes back (deferral ended) resumes its old branch.
	wt, conflicts, s := o.prepareWorktree(keep, id, br)
	if s != nil {
		return s
	}
	o.footprintWorktree(id, wt)
	if conflicts {
		// A worker is told not to rebase, so it would work on a stale base and its merge would end
		// in MERGE_CONFLICT anyway: set the ticket aside until its branch is rebased by hand.
		o.appendNotes(keep, id, fmt.Sprintf("Orchestra: %s conflicts with %s, so no worker was started on it. "+
			"Rebase it by hand (cd %s && git rebase %s, resolve, git rebase --continue), "+
			"then bring it back with: bd undefer %s",
			br, c.Base, wt, c.Base, id))
		if err := o.deferAside(keep, id, fmt.Sprintf("%s conflicts with %s; rebase it in %s", br, c.Base, wt)); err != nil {
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  DEFER_FAILED: %s conflicts with %s, and bd could not defer %s%s; kept out of this run, rebase it in %s",
				br, c.Base, id, because(err), wt)})
			return nil
		}
		o.emit(Event{Kind: EvDeferred, Ticket: id, Detail: "its branch conflicts with " + c.Base, Text: fmt.Sprintf(
			"  REBASE_FAILED: %s conflicts with %s -> %s deferred without starting a worker; "+
				"rebase it in %s, then bd undefer %s",
			br, c.Base, id, wt, id)})
		return nil
	}

	// A Claude worker gets the project's MCP servers and no others. Without them it would start with
	// every server on the machine, so a file it can't have stops the run rather than the worker.
	mcpArgs, err := o.mcpArgs(wt)
	if e := escapeOf(err); e != nil {
		return o.setAsideEscaped(keep, id, wt, e)
	}
	if err != nil {
		return halt(ExitTool, stopStartFailed, " for %s: %v", id, err).causedBy(err)
	}

	// Claude starts with its prompt already submitted, so nothing is pasted into its input box.
	// Herdr can only pass a one-line argument, so the prompt goes in a file the worker reads.
	prompt := strings.ReplaceAll(o.prompt, "TICKET_ID", id) + o.scopeNote(id) + o.budgetNote()
	launch := ""
	if c.LaunchPrompt && c.AgentKind == "claude" {
		launch, err = project.WriteLaunchPrompt(wt, id, prompt)
		if e := escapeOf(err); e != nil {
			return o.setAsideEscaped(keep, id, wt, e)
		}
		if err != nil {
			o.log.Raw("", fmt.Errorf("cannot write the launch prompt for %s, pasting it instead: %w", id, err))
			launch = ""
		}
	}

	// A Claude worker reports each tool it uses, through hooks loaded for it alone.
	var report []string
	if o.reporter != nil && c.AgentKind == "claude" {
		report, err = o.reporter.ReportArgs(wt)
		if e := escapeOf(err); e != nil {
			return o.setAsideEscaped(keep, id, wt, e)
		}
		if err != nil {
			o.log.Raw("", fmt.Errorf("cannot set up %s's worker to report what it does: %w", id, err))
			report = nil
		}
	}

	head := o.checkout.Head(ctx, c.Repo, br) // a worker that commits moves it

	tab, pane, err := o.tabs.CreateTab(ctx, c.Workspace, wt, id)
	if ctx.Err() != nil {
		if err == nil {
			o.closeTab(keep, tab)
		}
		return errInterrupted
	}
	if err != nil {
		return halt(ExitTool, stopTabFailed, " for %s (is '%s' a valid workspace?)", id, c.Workspace).causedBy(err)
	}
	started := time.Now()
	o.setActive(Status{Ticket: id, Title: t.Title, Tab: tab, Started: started})
	o.status(Status{Ticket: id, Title: t.Title, Tab: tab, Started: started, Agent: "starting"})
	defer o.status(Status{Ticket: id, Gone: true})

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
			return errInterrupted
		}
		if o.starter.IsNameRefused(err) {
			return nameRefused(err)
		}
		if ok = err == nil; !ok && typed {
			// The typed command may still be starting (a slow first start, many MCP servers), and
			// a second agent started in its pane would take the prompt twice: watch the pane for
			// longer, and start another only once it is plainly empty.
			o.log.Raw("", fmt.Errorf(
				"%s's worker was not named %s after it was launched (%v); watching its tab for longer", id, agent, err))
			held, adopted, err := o.adoptLate(ctx, pane, agent)
			if ctx.Err() != nil {
				return errInterrupted
			}
			if o.starter.IsNameRefused(err) {
				return nameRefused(err)
			}
			if ok = adopted; ok {
				o.info("  %s's worker was slow to start; named it %s", id, agent)
			} else if held != "" {
				return halt(ExitTool, stopStartFailed,
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
	// the agent is there before trying again.
	for attempt := 0; attempt < 10 && !ok; attempt++ {
		args := startArgs()
		err := o.starter.StartAgent(ctx, agent, c.AgentKind, pane, args)
		if ok = err == nil; ok {
			break
		}
		if o.starter.IsNameRefused(err) {
			return nameRefused(err)
		}
		o.log.Raw("", err)
		if o.starter.IsArgumentRefused(err) && len(args) > len(fixed) {
			// Start it plainly: paste the prompt instead, and do without reports if need be.
			if launch != "" {
				launch = ""
			} else {
				report = nil
			}
			continue
		}
		if o.starter.IsArgumentRefused(err) && len(mcpArgs) > 0 {
			// Without them it would start with every MCP server on the machine.
			return halt(ExitTool, stopStartFailed,
				" for %s in tab %s: Herdr refused the arguments giving it its MCP servers (%v)", id, tab, err).causedBy(err)
		}
		if !sleep(ctx, orDefault(o.wait.startRetry, 3*time.Second)) {
			return errInterrupted
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
						return nameRefused(err)
					}
					o.log.Raw("", err)
				}
			}
		}
		switch st {
		case StateIdle, StateDone:
			ok = true
		case StateWorking, StateBlocked, StateUnknown:
			// Up but busy. With the prompt given at launch that means it started; otherwise (a
			// startup dialog, say) give it time rather than starting a second one.
			ok = launch != "" || o.starter.WaitReady(ctx, agent)
		}
	}
	if !ok {
		return halt(ExitTool, stopStartFailed, " for %s in tab %s", id, tab)
	}

	w := o.newWatcher(wt, Status{Ticket: id, Title: t.Title, Tab: tab, Started: started})
	stopWatch := o.watch(ctx, w)
	defer stopWatch()

	if !o.promptTaken(ctx, id, agent, prompt, launch != "") {
		if ctx.Err() != nil {
			return errInterrupted
		}
		o.appendNotes(keep, id, fmt.Sprintf("Orchestra: the worker in Herdr tab %s never started on its prompt; "+
			"deferred so it can be retried (worktree %s).", tab, wt))
		if err := o.deferAside(keep, id, "the worker never started on its prompt"); err != nil {
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  DEFER_FAILED: %s's worker never started on its prompt, and bd could not defer it%s; "+
					"kept out of this run, worktree %s and tab %s left open",
				id, because(err), wt, tab)})
			return nil
		}
		o.emit(Event{Kind: EvDeferred, Ticket: id, Detail: "its worker never started on the prompt", Text: fmt.Sprintf(
			"  PROMPT_FAILED: %s's worker never started on its prompt -> deferred; worktree %s and tab %s left open",
			id, wt, tab)})
		return nil
	}

	stopWatch() // the settle loop reports from here on
	// It has begun on its prompt now, and reports through hooks only if it was started with them.
	return o.conclude(ctx, t, agent, tab, wt, head, started, report != nil, time.Time{}, w.report, how)
}

// adopt takes on the worker an asked ticket left in its tab, which carries on with the ticket now
// its question is answered, as if it had just been started on it: it waits for it to settle and
// merges or sets aside its work as usual. st is its status, as just read, at adopted; how is set as
// in work.
func (o *Loop) adopt(ctx context.Context, t Ticket, agent string, w askedWorker, st AgentState, adopted time.Time,
	how *settling) *stopReason {
	id := t.ID
	o.footprintWorktree(id, w.wt)
	head := o.checkout.Head(ctx, o.cfg.Repo, "wt/"+id)
	started := time.Now()
	base := Status{Ticket: id, Title: t.Title, Tab: w.tab, Started: started}
	o.setActive(base)
	running := base
	running.Agent = st
	o.status(running)
	defer o.status(Status{Ticket: id, Gone: true})
	// Its hooks' record may still end with the Stop of the turn it asked in, which would pass for the
	// end of this one: only what they reported once it was adopted counts.
	return o.conclude(ctx, t, agent, w.tab, w.wt, head, started, w.hooks, adopted, o.newWatcher(w.wt, base).report, how)
}

// budgetNote tells a worker the time its ticket has when the run sets a ticket limit, as agents pace
// themselves to a stated budget; "" without one.
func (o *Loop) budgetNote() string {
	limit := o.cfg.TicketLimit
	if limit <= 0 {
		return ""
	}
	return fmt.Sprintf("\n\nYou have about %s for this ticket: a worker still going after that stops the run, "+
		"so pace yourself to it.\n", ShortDuration(limit))
}

// resume tells an asked ticket's earlier worker, idle in its tab, that its question is answered and
// to carry on, so it keeps what it already knows of the ticket. It reports whether it took that up.
func (o *Loop) resume(ctx context.Context, id, agent string, w askedWorker) bool {
	msg := fmt.Sprintf("Orchestra: your question %s is answered. Read the answer with bd show %s, "+
		"claim %s again with bd update %s --claim, and carry on with it where you left off, as your instructions say.",
		w.question, w.question, id, id)
	if o.deliverPrompt(ctx, agent, msg) {
		return true
	}
	if ctx.Err() == nil {
		o.log.Raw("", fmt.Errorf("%s's earlier worker in tab %s did not take up the answer to %s; starting a new one",
			id, w.tab, w.question))
	}
	return false
}

// conclude waits for ticket t's worker, started (or adopted) at started, to settle, and then does
// what its ticket's status says: merge it, set it aside, or stop the run. hooks says it reports
// through them, and since from when (zero: any report counts). It sets how to how the worker
// settled, as work does.
func (o *Loop) conclude(ctx context.Context, t Ticket, agent, tab, wt, head string, started time.Time, hooks bool,
	since time.Time, report func(ctx context.Context, st AgentState, err error), how *settling) *stopReason {
	id, br := t.ID, "wt/"+t.ID
	idleAt, stop := o.waitSettled(ctx, id, agent, tab, wt, started, time.Now(), hooks, since, report)
	if stop != nil {
		return stop
	}

	// Beads, not the worker's own report, decides what happened. A ticket blocked on an open
	// question is out of the queue until the maintainer answers; the run goes on without it. The
	// worker has settled: what follows is bookkeeping, on keep, but for the waits merging has (a
	// check, a worker resolving conflicts) and evidence for triage, which Ctrl+C skips.
	keep := context.WithoutCancel(ctx)
	info, showErr := o.tickets.Show(keep, id)
	*how = settledSlow
	if q := OpenQuestion(info); q != nil && info.Status != "closed" {
		o.markAside(id)
		o.setAsked(id, &askedWorker{tab: tab, wt: wt, question: q.ID, title: q.Title, hooks: hooks})
		if info.Status != "open" { // back in the queue once answered
			if err := o.notes.Reopen(keep, id); err != nil {
				o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
					"  REOPEN_FAILED: %s stays %s, so it won't come back once %s is answered%s; "+
						"reopen it with: bd update %s --status open",
					id, info.Status, q.ID, because(err), id)})
			}
		}
		o.emit(Event{Kind: EvAsked, Ticket: id, Detail: q.ID + ": " + q.Title, Text: fmt.Sprintf(
			"  ASKED: %s waits on your answer to %s (%s); answer with: bd human respond %s; worktree %s and tab %s left open",
			id, q.ID, q.Title, q.ID, wt, tab)})
		return nil
	}
	switch s := info.Status; outcomeOf(s) {
	case outcomeClosed:
		if s := o.finish(ctx, id, br, wt, tab); s != nil {
			return s
		}
	case outcomeDeferred:
		o.markAside(id)
		o.emit(Event{Kind: EvDeferred, Ticket: id, Detail: "by the worker", Text: fmt.Sprintf(
			"  %s deferred by worker; worktree %s and tab %s left open", id, wt, tab)})
		o.queueTriage(ctx, o.gatherDeferral(ctx, id, t.Title, "the worker deferred it", wt))
	case outcomePaused:
		// Most likely waiting for an answer: stop rather than start the next ticket around it.
		o.appendNotes(keep, id, fmt.Sprintf(
			"Orchestra: worker in Herdr tab %s went idle with the ticket still in_progress (worktree %s).", tab, wt))
		return halt(ExitStuck, stopPaused,
			": %s still in_progress in tab %s (worktree %s); stopping so it can be answered", id, tab, wt)
	case outcomeUnreadable:
		return halt(ExitTool, stopStatusUnreadable,
			" for %s%s; stopping rather than guessing (worktree %s and tab %s left open)",
			id, because(showErr), wt, tab).causedBy(showErr)
	case outcomeUnfinished:
		if o.failedAtOnce(keep, s, started, idleAt, br, head, wt) {
			*how = settledFast
		}
		o.appendNotes(keep, id, fmt.Sprintf("Orchestra: worker in Herdr tab %s settled with the ticket still '%s'; "+
			"deferred for review (worktree %s).", tab, s, wt))
		why := fmt.Sprintf("worker finished without closing; see Herdr tab %s and worktree %s", tab, wt)
		if err := o.deferAside(keep, id, why); err != nil {
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  DEFER_FAILED: %s still %s, and bd could not defer it%s; "+
					"kept out of this run, worktree %s and tab %s left for review",
				id, s, because(err), wt, tab)})
			return nil
		}
		o.emit(Event{Kind: EvDeferred, Ticket: id, Detail: "still " + s + ", noted for review", Text: fmt.Sprintf(
			"  %s still %s -> noted and deferred; worktree %s and tab %s left open", id, s, wt, tab)})
		settled := fmt.Sprintf("the worker settled with the ticket still '%s', so the orchestrator deferred it", s)
		o.queueTriage(ctx, o.gatherDeferral(ctx, id, t.Title, settled, wt))
	}
	return nil
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

func outcomeOf(status string) outcome {
	switch status {
	case "closed":
		return outcomeClosed
	case "deferred":
		return outcomeDeferred
	case "in_progress":
		return outcomePaused
	case "unknown":
		return outcomeUnreadable
	}
	return outcomeUnfinished
}

// closedOutcome starts at one so an unset outcome is no outcome, never "merge".
type closedOutcome int

const (
	closedMerge closedOutcome = iota + 1
	closedNoCommit
	closedDirty
)

// A closed ticket is merged only with a commit naming it and a clean worktree.
func closedOutcomeOf(commit string, worktreeDirty bool) closedOutcome {
	switch {
	case commit == "":
		return closedNoCommit
	case worktreeDirty:
		return closedDirty
	}
	return closedMerge
}

// closeTab closes a worker's tab; a failure is only logged, as the tab is then merely left open.
func (o *Loop) closeTab(ctx context.Context, tab string) {
	if err := o.tabs.CloseTab(ctx, tab); err != nil {
		o.log.Raw("", fmt.Errorf("cannot close tab %s: %w", tab, err))
	}
}

// escapeOf is the *project.EscapeError in err's chain, or nil.
func escapeOf(err error) *project.EscapeError {
	var e *project.EscapeError
	if errors.As(err, &e) {
		return e
	}
	return nil
}

// setAsideEscaped sets ticket id aside without starting a worker: part of the path to its run files
// in worktree wt leads out of the worktree through a symlink, which orchestra won't follow (see
// project.OpenRun). The worker's own files are its to change, so only the user can say whether the
// link is safe to remove.
func (o *Loop) setAsideEscaped(ctx context.Context, id, wt string, e *project.EscapeError) *stopReason {
	o.log.Raw("", fmt.Errorf("%s: %w", id, e))
	why := fmt.Sprintf("its worktree's %s points outside the worktree", e.Path)
	o.appendNotes(ctx, id, fmt.Sprintf("Orchestra: %s (%s), so no worker was started on it: "+
		"orchestra writes a worker's run files only inside its worktree. "+
		"Look at what it points to, remove the link (rm %s), then bring it back with: bd undefer %s",
		why, filepath.Join(wt, e.Path), filepath.Join(wt, e.Path), id))
	if err := o.deferAside(ctx, id, why); err != nil {
		o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
			"  DEFER_FAILED: %s, and bd could not defer %s%s; kept out of this run, remove the link in %s",
			why, id, because(err), wt)})
		return nil
	}
	o.emit(Event{Kind: EvDeferred, Ticket: id, Detail: why, Text: fmt.Sprintf(
		"  RUN_FILES_OUTSIDE: %s -> %s deferred without starting a worker; remove the link %s, then bd undefer %s",
		why, id, filepath.Join(wt, e.Path), id)})
	return nil
}
