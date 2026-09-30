package dispatch

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// work runs one ticket from worktree to merge. It returns a reason when the run must stop; the
// ticket then stays listed as active (still being worked on).
func (o *Loop) work(ctx context.Context, t Ticket) (stop *stopReason) {
	c := o.cfg
	id, br := t.ID, "wt/"+t.ID
	defer func() {
		if stop == nil {
			o.clearActive(id)
		}
	}()
	o.setAsked(id, false) // back from a question: set aside again, it stays out
	if HasLabel(t, UnmergedLabel) {
		o.setLabelled(id, true) // reopened after an earlier run left it unmerged: merging removes the label
	}

	// One worktree per ticket. A ticket that comes back (deferral ended) resumes its old branch.
	wt, conflicts, s := o.prepareWorktree(id, br)
	if s != nil {
		return s
	}
	o.footprintWorktree(id, wt)
	if conflicts {
		// A worker is told not to rebase, so it would work on a stale base and its merge would end
		// in MERGE_CONFLICT anyway: set the ticket aside until its branch is rebased by hand.
		o.appendNotes(id, fmt.Sprintf("Orchestra: %s conflicts with %s, so no worker was started on it. Rebase it by hand (cd %s && git rebase %s, resolve, git rebase --continue), then bring it back with: bd undefer %s",
			br, c.Base, wt, c.Base, id))
		if err := o.deferAside(id, fmt.Sprintf("%s conflicts with %s; rebase it in %s", br, c.Base, wt)); err != nil {
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  DEFER_FAILED: %s conflicts with %s, and bd could not defer %s%s; kept out of this run, rebase it in %s",
				br, c.Base, id, because(err), wt)})
			return nil
		}
		o.emit(Event{Kind: EvDeferred, Ticket: id, Detail: "its branch conflicts with " + c.Base, Text: fmt.Sprintf(
			"  REBASE_FAILED: %s conflicts with %s -> %s deferred without starting a worker; rebase it in %s, then bd undefer %s",
			br, c.Base, id, wt, id)})
		return nil
	}

	// The worker's Herdr name: Herdr takes fewer characters than a ticket ID can hold.
	agent := o.agentName(id)

	// A returning ticket's earlier worker may still sit in its tab under the ticket's name, which
	// Herdr keeps unique. Rename it so the new worker can have the name; its tab stays as it is.
	st, err := o.readStatus(ctx, agent, 5)
	if ctx.Err() != nil {
		return errInterrupted
	}
	switch st {
	case "gone":
	case "unreadable":
		return halt(ExitTool, "HERDR_FAILED: cannot tell whether an earlier worker for %s is still in its tab: %v", id, err)
	case "working", "blocked":
		return halt(ExitTool, "AGENT_BUSY: an earlier worker for %s is still %s in its tab; stopping rather than starting a second one on %s", id, st, wt)
	default:
		if name := o.namer.FreeName(agent); name == "" || o.namer.RenameAgent(agent, name) != nil {
			return halt(ExitTool, "AGENT_NAME_TAKEN: an earlier worker for %s holds its name and could not be renamed", id)
		} else {
			o.info("  earlier worker for %s renamed to %s; its tab is left open", id, name)
		}
	}

	tab, pane, err := o.tabs.CreateTab(c.Workspace, wt, id)
	if err != nil {
		return halt(ExitTool, "TAB_FAILED for %s (is '%s' a valid workspace?)", id, c.Workspace)
	}
	started := time.Now()
	o.setActive(Status{Ticket: id, Title: t.Title, Tab: tab, Started: started})
	o.status(Status{Ticket: id, Title: t.Title, Tab: tab, Started: started, Agent: "starting"})
	defer o.status(Status{Ticket: id, Gone: true})

	// Claude starts with its prompt already submitted, so nothing is pasted into its input box.
	// Herdr can only pass a one-line argument, so the prompt goes in a file the worker reads.
	prompt := strings.ReplaceAll(o.prompt, "TICKET_ID", id)
	launch := ""
	if c.LaunchPrompt && c.AgentKind == "claude" {
		var err error
		if launch, err = project.WriteLaunchPrompt(wt, id, prompt); err != nil {
			o.log.Raw("", fmt.Errorf("cannot write the launch prompt for %s, pasting it instead: %w", id, err))
			launch = ""
		}
	}

	// A Claude worker reports each tool it uses, through hooks loaded for it alone.
	var report []string
	if o.reporter != nil && c.AgentKind == "claude" {
		var err error
		if report, err = o.reporter.ReportArgs(wt); err != nil {
			o.log.Raw("", fmt.Errorf("cannot set up %s's worker to report what it does: %w", id, err))
			report = nil
		}
	}

	// With its prompt in a file, the worker is started by typing the command into the tab and
	// named once Herdr recognises it. Herdr's own start waits for the agent to look ready for
	// input, which a worker that goes straight to work never does, so it could only time out.
	// A name Herdr refuses would be refused on every retry, so that ends the attempt at once.
	nameRefused := func(err error) *stopReason {
		return halt(ExitTool, "START_FAILED for %s in tab %s: Herdr refused the agent name %s (%v)", id, tab, agent, err)
	}
	ok := false
	if launch != "" {
		err := o.starter.LaunchInPane(pane, c.AgentKind, append(report[:len(report):len(report)], launch))
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
			o.log.Raw("", fmt.Errorf("%s's worker was not named %s after it was launched (%v); watching its tab for longer", id, agent, err))
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
				return halt(ExitTool, "START_FAILED for %s in tab %s: the worker launched there could not be named %s (%s); stopping rather than starting a second one in it", id, tab, agent, held)
			}
		}
		if !ok {
			o.log.Raw("", fmt.Errorf("%s's worker could not be started from its prompt file and named %s (%v); starting it with herdr agent start and pasting the prompt", id, agent, err))
			launch = ""
		}
	}

	// 'agent start' can report failure while the agent is still coming up (agent_not_ready keeps
	// the name), and a retry then finds the pane occupied, so after each failure check whether
	// the agent is there before trying again.
	for attempt := 0; attempt < 10 && !ok; attempt++ {
		args := report[:len(report):len(report)]
		if launch != "" {
			args = append(args, launch)
		}
		err := o.starter.StartAgent(ctx, agent, c.AgentKind, pane, args)
		if ok = err == nil; ok {
			break
		}
		o.log.Raw("", err)
		if o.starter.IsNameRefused(err) {
			return nameRefused(err)
		}
		if o.starter.IsArgumentRefused(err) && len(args) > 0 {
			// Start it plainly: paste the prompt instead, and do without reports if need be.
			if launch != "" {
				launch = ""
			} else {
				report = nil
			}
			continue
		}
		if !sleep(ctx, orDefault(o.wait.startRetry, 3*time.Second)) {
			return errInterrupted
		}
		st, err := o.agents.Status(agent)
		if err != nil {
			o.log.Raw("", err)
		}
		if st == "gone" {
			// A start that times out leaves the agent running unnamed in its pane, and a retry
			// would find the pane busy: adopt that agent under the ticket's name instead.
			if name, kind, pst := o.namer.PaneAgent(pane); pst != "gone" && name == "" && kind == c.AgentKind {
				if err := o.namer.RenameAgent(pane, agent); err == nil {
					o.info("  %s's worker started without its name; named it %s", id, agent)
					st = pst
				} else {
					o.log.Raw("", err)
					if o.starter.IsNameRefused(err) {
						return nameRefused(err)
					}
				}
			}
		}
		switch st {
		case "idle", "done":
			ok = true
		case "working", "blocked", "unknown":
			// Up but busy. With the prompt given at launch that means it started; otherwise (a
			// startup dialog, say) give it time rather than starting a second one.
			ok = launch != "" || o.starter.WaitReady(ctx, agent)
		}
	}
	if !ok {
		return halt(ExitTool, "START_FAILED for %s in tab %s", id, tab)
	}

	w := o.newWatcher(wt, Status{Ticket: id, Title: t.Title, Tab: tab, Started: started})
	stopWatch := o.watch(ctx, w)
	defer stopWatch()

	if !o.promptTaken(ctx, id, agent, prompt, launch != "") {
		if ctx.Err() != nil {
			return errInterrupted
		}
		o.appendNotes(id, fmt.Sprintf("Orchestra: the worker in Herdr tab %s never started on its prompt; deferred so it can be retried (worktree %s).", tab, wt))
		if err := o.deferAside(id, "the worker never started on its prompt"); err != nil {
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  DEFER_FAILED: %s's worker never started on its prompt, and bd could not defer it%s; kept out of this run, worktree %s and tab %s left open",
				id, because(err), wt, tab)})
			return nil
		}
		o.emit(Event{Kind: EvDeferred, Ticket: id, Detail: "its worker never started on the prompt", Text: fmt.Sprintf(
			"  PROMPT_FAILED: %s's worker never started on its prompt -> deferred; worktree %s and tab %s left open", id, wt, tab)})
		return nil
	}

	stopWatch() // the settle loop reports from here on
	if stop := o.waitSettled(ctx, id, agent, tab, wt, started, w.report); stop != nil {
		return stop
	}

	// Beads, not the worker's own report, decides what happened. A ticket blocked on an open
	// question is out of the queue until the maintainer answers; the run goes on without it.
	info, showErr := o.tickets.Show(id)
	if q := OpenQuestion(info); q != nil && info.Status != "closed" {
		o.markAside(id)
		o.setAsked(id, true)
		if info.Status != "open" { // back in the queue once answered
			if err := o.notes.Reopen(id); err != nil {
				o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
					"  REOPEN_FAILED: %s stays %s, so it won't come back once %s is answered%s; reopen it with: bd update %s --status open",
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
		o.queueTriage(ctx, o.gatherDeferral(id, t.Title, "the worker deferred it", wt))
	case outcomePaused:
		// Most likely waiting for an answer: stop rather than start the next ticket around it.
		o.appendNotes(id, fmt.Sprintf("Orchestra: worker in Herdr tab %s went idle with the ticket still in_progress (worktree %s).", tab, wt))
		return halt(ExitStuck, "PAUSED: %s still in_progress in tab %s (worktree %s); stopping so it can be answered", id, tab, wt)
	case outcomeUnreadable:
		return halt(ExitTool, "STATUS_UNREADABLE for %s%s; stopping rather than guessing (worktree %s and tab %s left open)", id, because(showErr), wt, tab)
	case outcomeUnfinished:
		o.appendNotes(id, fmt.Sprintf("Orchestra: worker in Herdr tab %s settled with the ticket still '%s'; deferred for review (worktree %s).", tab, s, wt))
		if err := o.deferAside(id, fmt.Sprintf("worker finished without closing; see Herdr tab %s and worktree %s", tab, wt)); err != nil {
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  DEFER_FAILED: %s still %s, and bd could not defer it%s; kept out of this run, worktree %s and tab %s left for review",
				id, s, because(err), wt, tab)})
			return nil
		}
		o.emit(Event{Kind: EvDeferred, Ticket: id, Detail: "still " + s + ", noted for review", Text: fmt.Sprintf(
			"  %s still %s -> noted and deferred; worktree %s and tab %s left open", id, s, wt, tab)})
		o.queueTriage(ctx, o.gatherDeferral(id, t.Title, fmt.Sprintf("the worker settled with the ticket still '%s', so the orchestrator deferred it", s), wt))
	}
	return nil
}

type outcome int

const (
	outcomeClosed outcome = iota
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

type closedOutcome int

const (
	closedMerge closedOutcome = iota
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
