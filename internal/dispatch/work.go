package dispatch

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// work runs one ticket from worktree to merge. It returns a reason when the run must stop; the
// ticket then stays listed as active (still being worked on), or, back from a question before its
// earlier worker is told anything, asked. It sets how to how the worker settled, for the hold for
// the environment. A panic in it is such a reason: PANIC.
func (o *Loop) work(ctx context.Context, t Ticket, how *settling) (stop *stopReason) {
	c := o.cfg
	id, br := t.ID, branchOf(t.ID)
	defer func() {
		if p := recover(); p != nil {
			stop = o.panicStop(id, p)
		} else if stop == nil {
			o.clearActive(id)
		}
	}()
	// Back from a question, the ticket stays asked, for a stop or Ctrl+C to leave it as it was (see
	// leaveAsked, saveCarried), until its earlier worker is told to carry on or adopted (see takeOn),
	// or a new worker is to take it.
	earlier, asked := o.asked(id)
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
	// Herdr keeps unique, or, carried over from a run cut short as it started it, unnamed in its pane
	// (see earlierState). It is looked at before the worktree is touched: one still at work there
	// must not have its branch rebased under it. One left by a question asked in this run may have
	// had its answer in its tab and carried on: it is adopted, or, idle, told the answer is in. Any
	// other is renamed so the new worker can have the name; its tab stays as it is.
	st, err := o.earlierState(ctx, id, earlier, 5)
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
		if asked && (st == StateIdle || st == StateDone) {
			// Told to carry on, it may take that up however resume ends, Ctrl+C included: it carries the
			// ticket from now on, as one adopted does.
			o.takeOn(t, earlier)
			if o.resume(ctx, id, agent, earlier) {
				o.info("  %s's earlier worker in tab %s was told %s and carries on; adopting it",
					id, earlier.tab, earlier.told())
				return o.adopt(ctx, t, agent, earlier, StateWorking, adopted, how)
			}
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
	o.setAsked(id, nil) // back from a question: set aside again once this worker settles, it stays out

	// One worktree per ticket. A ticket that comes back (deferral ended) resumes its old branch.
	wt, conflicts, s := o.prepareWorktree(keep, id, br)
	if s != nil {
		return s
	}
	o.footprintWorktree(id, wt)
	if conflicts {
		// A worker is told not to rebase, so it would work on a stale base and its merge would end
		// in MERGE_CONFLICT anyway: set the ticket aside until its branch is rebased by hand.
		o.putAside(keep, t, asideTexts{
			note: fmt.Sprintf("Orchestra: %s conflicts with %s, so no worker was started on it. "+
				"Rebase it by hand (cd %s && git rebase %s, resolve, git rebase --continue), "+
				"then bring it back with: bd undefer %s",
				br, c.Base, wt, c.Base, id),
			reason: fmt.Sprintf("%s conflicts with %s; rebase it in %s", br, c.Base, wt),
			detail: "its branch conflicts with " + c.Base,
			deferred: fmt.Sprintf("  REBASE_FAILED: %s conflicts with %s -> %s deferred without starting a worker; "+
				"rebase it in %s, then bd undefer %s",
				br, c.Base, id, wt, id),
			failed: fmt.Sprintf("%s conflicts with %s, and bd could not defer %s", br, c.Base, id),
			then:   "rebase it in " + wt,
		})
		return nil
	}
	// Back from a question with its earlier worker gone from its tab, the ticket has that worker's
	// session resumed, once in a run, rather than a new worker starting over (see resume.go).
	if asked && st == StateGone {
		if s, ok := o.sessionOf(wt, earlier.hooks); ok && o.firstResume(id) {
			return o.resumeGone(ctx, t, wt, earlier.tab, earlier.question, true, s, how)
		}
	}

	// A Claude worker gets the project's MCP servers and no others. Without them it would start with
	// every server on the machine, so a file it can't have stops the run rather than the worker.
	mcpArgs, err := o.mcpArgs(wt)
	if e := escapeOf(err); e != nil {
		return o.setAsideEscaped(keep, t, wt, e)
	}
	if err != nil {
		return halt(ExitTool, stopStartFailed, " for %s: %v", id, err).causedBy(err)
	}

	// A Claude worker's standing rules go in its system prompt, which survives compaction (see rules.go).
	rules, err := o.rulesArgs(wt, id)
	if e := escapeOf(err); e != nil {
		return o.setAsideEscaped(keep, t, wt, e)
	}
	if err != nil {
		o.log.Raw("", fmt.Errorf("%s's worker gets its whole prompt as its first message: %w", id, err))
		rules = nil
	}
	left := o.earlierNote(ctx, br, wt)
	full := o.fullPrompt(id, left)
	prompt := full
	if rules != nil {
		prompt = ticketPrompt(id, left)
	}

	// Claude starts with its prompt already submitted, so nothing is pasted into its input box.
	// Herdr can only pass a one-line argument, so the prompt goes in a file the worker reads.
	launch := ""
	if c.LaunchPrompt && c.ClaudeWorkers() {
		launch, err = project.WriteLaunchPrompt(wt, id, prompt)
		if e := escapeOf(err); e != nil {
			return o.setAsideEscaped(keep, t, wt, e)
		}
		if err != nil {
			o.log.Raw("", fmt.Errorf("cannot write the launch prompt for %s, pasting it instead: %w", id, err))
			launch = ""
		}
	}

	// A Claude worker reports each tool it uses, through hooks loaded for it alone.
	var report []string
	if o.reporter != nil && c.ClaudeWorkers() {
		o.newWorkerHooks(id, wt)
		report, err = o.reporter.ReportArgs(wt)
		if e := escapeOf(err); e != nil {
			return o.setAsideEscaped(keep, t, wt, e)
		}
		if err != nil {
			o.log.Raw("", fmt.Errorf("cannot set up %s's worker to report what it does: %w", id, err))
			report = nil
		}
	}

	head := o.checkout.Head(ctx, c.Repo, br) // a worker that commits moves it

	launched, stop := o.startWorker(ctx, t, agent, wt, workerArgs{mcp: mcpArgs, rules: rules, report: report}, launch, "")
	if stop != nil {
		return stop
	}
	if !launched.rules {
		prompt = full // started without its rules, as Herdr refused them: they are pasted with the rest
	}
	defer o.status(Status{Ticket: id, Gone: true})
	tab, started := launched.tab, launched.started

	w := o.newWatcher(wt, Status{Ticket: id, Title: t.Title, Tab: tab, Started: started})
	stopWatch := o.watch(ctx, w)
	defer stopWatch()

	if !o.promptTaken(ctx, id, agent, prompt, launched.atLaunch, false) {
		if ctx.Err() != nil {
			return errInterrupted
		}
		o.putAside(keep, t, asideTexts{
			note: fmt.Sprintf("Orchestra: the worker in Herdr tab %s never started on its prompt; "+
				"deferred so it can be retried (worktree %s).", tab, wt),
			reason: "the worker never started on its prompt",
			detail: "its worker never started on the prompt",
			deferred: fmt.Sprintf("  PROMPT_FAILED: %s's worker never started on its prompt -> deferred; "+
				"worktree %s and tab %s left open", id, wt, tab),
			failed: id + "'s worker never started on its prompt, and bd could not defer it",
			then:   fmt.Sprintf("worktree %s and tab %s left open", wt, tab),
		})
		return nil
	}

	stopWatch() // the settle loop reports from here on
	// It has begun on its prompt now, and reports through hooks only if it was started with them.
	return o.conclude(ctx, t, worker{id: id, br: br, wt: wt, tab: tab, agent: agent, started: started,
		hooks: launched.hooks}, head, w.report, how)
}

// adopt takes on the worker an asked ticket left in its tab, which carries on with the ticket now
// its question is answered, as if it had just been started on it: it waits for it to settle and
// merges or sets aside its work as usual. st is its status, as just read, at adopted; how is set as
// in work.
func (o *Loop) adopt(ctx context.Context, t Ticket, agent string, w askedWorker, st AgentState, adopted time.Time,
	how *settling) *stopReason {
	id := t.ID
	base := o.takeOn(t, w)
	o.footprintWorktree(id, w.wt)
	head := o.checkout.Head(ctx, o.cfg.Repo, branchOf(id))
	running := base
	running.Agent = st
	o.status(running)
	defer o.status(Status{Ticket: id, Gone: true})
	// Its hooks' record may still end with the Stop of the turn it asked in, which would pass for the
	// end of this one: only what they reported once it was adopted counts.
	return o.conclude(ctx, t, worker{id: id, br: branchOf(id), wt: w.wt, tab: w.tab, agent: agent,
		started: base.Started, hooks: w.hooks, since: adopted}, head, o.newWatcher(w.wt, base).report, how)
}

// takeOn makes the worker an asked ticket t left in its tab, w, the ticket's again, as it is told to
// carry on or adopted: placed, and active from now on, and no longer asked. A stop or Ctrl+C from
// then on leaves the ticket running, labelled (see leaveRunning) and carried over to the next run
// (see saveCarried), as its worker may close it once orchestra has gone. It returns the ticket's
// Status.
func (o *Loop) takeOn(t Ticket, w askedWorker) Status {
	id := t.ID
	o.place(id, askedWorker{tab: w.tab, pane: w.pane, wt: w.wt, hooks: w.hooks})
	st := Status{Ticket: id, Title: t.Title, Tab: w.tab, Started: time.Now()}
	o.setActive(st)
	o.setAsked(id, nil) // back from a question: set aside again once it settles, it stays out
	return st
}

// adoptAsked takes on the worker an asked ticket left in its tab, which claimed the ticket again or
// closed it there without it coming back through bd ready (see followAsked), as work takes on one
// that is still working when its ticket comes back. One idle with its ticket in progress is told
// the answer is in; one that closed it settles at once and its work is merged; one gone from its tab
// has its session resumed. It returns and sets how as work does.
func (o *Loop) adoptAsked(ctx context.Context, a adoption, how *settling) (stop *stopReason) {
	t, w := a.t, a.w
	id := t.ID
	defer func() {
		if p := recover(); p != nil {
			stop = o.panicStop(id, p)
		} else if stop == nil {
			o.clearActive(id)
		}
	}()
	// Its worker's from now on, so a stop or Ctrl+C before it settles leaves it labelled and carried
	// over: it is no longer asked.
	o.takeOn(t, w)
	if HasLabel(t, UnmergedLabel) {
		o.setLabelled(id, true) // reopened after an earlier run left it unmerged: merging removes the label
	}
	agent := o.agentName(id)
	if a.resume != nil {
		return o.resumeGone(ctx, t, w.wt, w.tab, w.question, false, *a.resume, how)
	}
	st, err := o.readStatus(ctx, agent, 5)
	adopted := time.Now() // its hooks reported anything older in its earlier turns
	if ctx.Err() != nil {
		return errInterrupted
	}
	if err != nil {
		return halt(ExitTool, stopHerdrFailed,
			": cannot tell what %s's worker is doing in tab %s: %v", id, w.tab, err).causedBy(err)
	}
	switch {
	case t.Status != StatusClosed && (st == StateIdle || st == StateDone) && o.resume(ctx, id, agent, w):
		o.info("  %s's worker in tab %s was told %s and carries on; adopting it", id, w.tab, w.told())
		st = StateWorking
	case ctx.Err() != nil:
		return errInterrupted
	default:
		o.info("  %s's worker in tab %s is %s with the ticket %s; adopting it", id, w.tab, st, t.Status)
	}
	return o.adopt(ctx, t, agent, w, st, adopted, how)
}

// budgetNote tells a worker the time its ticket has when the run sets a ticket limit, as agents pace
// themselves to a stated budget; "" without one.
func (o *Loop) budgetNote() string {
	limit := o.cfg.TicketLimit
	if limit <= 0 {
		return ""
	}
	return fmt.Sprintf("\n\nYou have about %s for this ticket: a worker still going after that stops the run, "+
		"so pace yourself to it.\n", command.ShortDuration(limit))
}

// resume tells an asked ticket's earlier worker, idle in its tab, that its question is answered and
// to carry on, so it keeps what it already knows of the ticket; one the last run left running, with
// no question, is told its ticket is back. It reports whether it took that up.
func (o *Loop) resume(ctx context.Context, id, agent string, w askedWorker) bool {
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
		o.log.Raw("", fmt.Errorf("%s's earlier worker in tab %s did not take up being told %s; starting a new one",
			id, w.tab, w.told()))
	}
	return false
}

// told is what resume tells the worker: "Q is answered", or, with no question, "to carry on".
func (w askedWorker) told() string {
	if w.question == "" {
		return "to carry on"
	}
	return w.question + " is answered"
}

// worker is a ticket's worker as the run waits on it and merges its work: the ticket, its branch
// and worktree, the Herdr tab it runs in and its name there, and how to read its hooks.
type worker struct {
	id, br, wt, tab, agent string
	started                time.Time // when it was started on the ticket, or adopted
	hooks                  bool      // it reports what it does through hooks
	since                  time.Time // its hooks' reports count from then on (zero: any report counts)
}

// conclude waits for ticket t's worker, w, to settle, and then does what its ticket's status says:
// merge it, set it aside, or stop the run. head is its branch's commit before the worker began. It
// sets how to how the worker settled, as work does.
func (o *Loop) conclude(ctx context.Context, t Ticket, w worker, head string,
	report func(ctx context.Context, st AgentState, err error), how *settling) *stopReason {
	id, br, wt, tab := w.id, w.br, w.wt, w.tab
	idleAt, stop := o.waitSettled(ctx, w, time.Now(), report)
	if stop != nil {
		return stop
	}

	// Beads, not the worker's own report, decides what happened. A ticket blocked on an open
	// question is out of the queue until the maintainer answers; the run goes on without it. The
	// worker has settled: what follows is bookkeeping, on keep, but for the waits merging has (a
	// check, a worker resolving conflicts) and evidence for triage, which Ctrl+C skips. bd failing
	// once, as when another bd holds the database, doesn't stop the run: the ticket is read again.
	keep := context.WithoutCancel(ctx)
	info, showErr := o.showTries(keep, id)
	*how = settledSlow
	if q := OpenQuestion(info); q != nil && info.Status != StatusClosed {
		o.markAside(id)
		o.setAsked(id, &askedWorker{tab: tab, wt: wt, question: q.ID, title: q.Title, hooks: w.hooks})
		if info.Status != StatusOpen { // back in the queue once answered
			if err := o.notes.Reopen(keep, id); err != nil {
				o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
					"  REOPEN_FAILED: %s stays %s, so it won't come back once %s is answered%s; "+
						"reopen it with: bd update %s --status open",
					id, info.Status, q.ID, because(err), id)})
			}
		}
		o.emit(Event{Kind: EvAsked, Ticket: id, Title: t.Title, Detail: q.ID + ": " + q.Title, Text: fmt.Sprintf(
			"  ASKED: %s waits on your answer to %s (%s); answer with: bd human respond %s; worktree %s and tab %s left open",
			id, q.ID, q.Title, q.ID, wt, tab)})
		return nil
	}
	switch s := info.Status; outcomeOf(s) {
	case outcomeClosed:
		if s := o.finish(ctx, w); s != nil {
			return s
		}
	case outcomeDeferred:
		o.markAside(id)
		o.emit(Event{Kind: EvDeferred, Ticket: id, Title: t.Title, Detail: "by the worker", Text: fmt.Sprintf(
			"  %s deferred by worker; worktree %s and tab %s left open", id, wt, tab)})
		o.triageDeferred(ctx, id, "the worker deferred it", wt)
	case outcomePaused:
		if o.workerGone(keep, w.agent) {
			return o.pausedGone(ctx, t, w, how)
		}
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
		if o.failedAtOnce(keep, s, w.started, idleAt, br, head, wt) {
			*how = settledFast
		}
		if !o.putAside(keep, t, asideTexts{
			note: fmt.Sprintf("Orchestra: worker in Herdr tab %s settled with the ticket still '%s'; "+
				"deferred for review (worktree %s).", tab, s, wt),
			reason:   fmt.Sprintf("worker finished without closing; see Herdr tab %s and worktree %s", tab, wt),
			detail:   "still " + string(s) + ", noted for review",
			deferred: fmt.Sprintf("  %s still %s -> noted and deferred; worktree %s and tab %s left open", id, s, wt, tab),
			failed:   fmt.Sprintf("%s still %s, and bd could not defer it", id, s),
			then:     fmt.Sprintf("worktree %s and tab %s left for review", wt, tab),
		}) {
			return nil
		}
		settled := fmt.Sprintf("the worker settled with the ticket still '%s', so the orchestrator deferred it", s)
		o.triageDeferred(ctx, id, settled, wt)
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

// closedOutcome starts at one so an unset outcome is no outcome, never "merge".
type closedOutcome int

const (
	closedMerge closedOutcome = iota + 1
	closedNoCommit
	closedDirty
	closedNoChange // nothing to merge: no commits of its own and a clean worktree
)

// A closed ticket is merged only with a commit naming it and a clean worktree. One with no commit
// naming it, whose branch has no commits beyond Base (own, -1 when git can't count them) and whose
// worktree is clean, closed with no change of its own: there is nothing to merge, or to review.
func closedOutcomeOf(commit string, own int, worktreeDirty bool) closedOutcome {
	switch {
	case commit == "" && own == 0 && !worktreeDirty:
		return closedNoChange
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
			why, link, link, id),
		reason: why,
		detail: why,
		deferred: fmt.Sprintf("  RUN_FILES_OUTSIDE: %s -> %s deferred without starting a worker; "+
			"remove the link %s, then bd undefer %s", why, id, link, id),
		failed: fmt.Sprintf("%s, and bd could not defer %s", why, id),
		then:   "remove the link in " + wt,
	})
	return nil
}
