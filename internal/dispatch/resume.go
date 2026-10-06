package dispatch

import (
	"context"
	"fmt"
	"time"
)

// A Claude worker's hooks record its Claude Code session (see Reporter.Session). A worker gone from
// its tab with its ticket still in progress (its tab closed, Claude Code quit or crashed, Herdr
// restarted) is started again in a new tab in its worktree, resuming that session: it has all it
// knew of the ticket, and is told to carry on. That happens on its own, once per ticket in a run,
// for a worker this run started or adopted, and for one the last run left behind; a worker gone
// again stops the run (PAUSED), and the next run resumes it once more. Its old tab is left as it is.
// A ticket whose worker should stay stopped is deferred before its tab is closed.
//
// A ticket back through bd ready from a question, its earlier worker gone, has that worker's session
// resumed the same way, once in a run, told the answer is in and to claim the ticket again: the
// answer is to what that session asked. A ticket back from a deferral gets a new worker, told what
// the earlier attempt left (see earlierNote): a deferral is a stop, by the worker giving up, by
// orchestra setting it aside, or by the maintainer stopping it, and the ticket may have been changed
// since, by triage or by hand; a new worker reads it as it is, without the reasoning that led there.

// sessionOf returns the session to resume of the worker in worktree wt, which reports through
// hooks or not, and false when there is none: a worker that isn't Claude, or one without hooks,
// records none.
func (o *Loop) sessionOf(wt string, hooks bool) (Session, bool) {
	if o.reporter == nil || !hooks || !o.cfg.ClaudeWorkers() {
		return Session{}, false
	}
	return o.reporter.Session(wt)
}

// firstResume reports whether ticket id's worker has not had its session resumed in this run yet,
// and notes that it has from now on.
func (o *Loop) firstResume(id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.resumed[id] {
		return false
	}
	if o.resumed == nil {
		o.resumed = map[string]bool{}
	}
	o.resumed[id] = true
	return true
}

// workerGone reports whether Herdr has no worker named agent; a status it can't read says nothing.
func (o *Loop) workerGone(ctx context.Context, agent string) bool {
	st, err := o.agents.Status(ctx, agent)
	return err == nil && st == StateGone
}

// pausedGone is conclude's outcome for ticket t, in progress with its worker w gone from its tab:
// its session resumed in a new tab, the first time in this run, or else the run stopped (PAUSED).
func (o *Loop) pausedGone(ctx context.Context, t Ticket, w worker, how *settling) *stopReason {
	id := t.ID
	s, ok := o.sessionOf(w.wt, w.hooks)
	if ok && o.firstResume(id) {
		return o.resumeGone(ctx, t, w.wt, w.tab, "", false, s, how)
	}
	o.appendNotes(context.WithoutCancel(ctx), id, fmt.Sprintf(
		"Orchestra: the worker in Herdr tab %s is gone, with the ticket still in_progress (worktree %s).", w.tab, w.wt))
	again := ""
	if ok {
		again = fmt.Sprintf("; its session %s was resumed once in this run already, and the next run resumes it again",
			s.ID)
	}
	return halt(ExitStuck, stopPaused, ": %s still in_progress, its worker gone from tab %s (worktree %s)%s; "+
		"stopping so it can be looked at", id, w.tab, w.wt, again)
}

// resumedInTab says asked ticket t, whose worker w is gone from its tab with the ticket in progress,
// comes back to have its session s resumed (see adoptAsked), and records its footprint; the caller
// counts it running.
func (o *Loop) resumedInTab(t Ticket, w askedWorker, s Session) adoption {
	o.setAsked(t.ID, nil)
	o.setParent(t.ID, t.Parent)
	o.startFootprint(t)
	return adoption{t: t, w: w, resume: &s}
}

// resumeGone starts ticket t's worker again, gone from tab with the ticket in progress, or back
// through bd ready, open, for it to claim again, in a new tab in its worktree wt, resuming its
// session s, and tells it to carry on; question is the one it was left waiting on, if any, answered
// since. The worker is then adopted, as one back from a question is (see adopt), its ticket limit
// counted afresh. It returns and sets how as work does.
func (o *Loop) resumeGone(ctx context.Context, t Ticket, wt, tab, question string, back bool, s Session,
	how *settling) *stopReason {
	c := o.cfg
	id := t.ID
	keep := context.WithoutCancel(ctx)
	note := fmt.Sprintf("Orchestra: the worker in Herdr tab %s is gone, with the ticket still in_progress (worktree %s); "+
		"its session %s is resumed in a new tab.", tab, wt, s.ID)
	text := fmt.Sprintf("  RESUMED: %s's worker is gone from tab %s with the ticket in progress; "+
		"resuming its session %s in a new tab (worktree %s)", id, tab, s.ID, wt)
	if back {
		note = fmt.Sprintf("Orchestra: the ticket is back, and its worker in Herdr tab %s is gone (worktree %s); "+
			"its session %s is resumed in a new tab.", tab, wt, s.ID)
		text = fmt.Sprintf("  RESUMED: %s is back, and its worker is gone from tab %s; "+
			"resuming its session %s in a new tab (worktree %s)", id, tab, s.ID, wt)
	}
	o.appendNotes(keep, id, note)
	o.emit(Event{Kind: EvWarn, Ticket: id, Title: t.Title, Detail: "its worker gone, its session resumed", Text: text})

	mcpArgs, err := o.mcpArgs(wt)
	if e := escapeOf(err); e != nil {
		return o.setAsideEscaped(keep, t, wt, e)
	}
	if err != nil {
		return halt(ExitTool, stopStartFailed, " for %s: %v", id, err).causedBy(err)
	}
	// The files the worker before it edited stay in the footprint (sessionOf found a reporter).
	o.newWorkerHooks(id, wt)
	report, err := o.reporter.ResumeArgs(wt)
	if e := escapeOf(err); e != nil {
		return o.setAsideEscaped(keep, t, wt, e)
	}
	if err != nil {
		o.log.Raw("", fmt.Errorf("cannot set up %s's resumed worker to report what it does: %w", id, err))
		report = nil
	}

	// Claude Code keeps no system prompt with the session: the standing rules are given again.
	rules, err := o.rulesArgs(wt, id)
	if e := escapeOf(err); e != nil {
		return o.setAsideEscaped(keep, t, wt, e)
	}
	if err != nil {
		o.log.Raw("", fmt.Errorf("%s's resumed worker goes without its standing rules in its system prompt: %w", id, err))
		rules = nil
	}

	msg := fmt.Sprintf("Orchestra: your worker stopped, its tab gone, with %s still in progress, "+
		"and this is your session resumed. Carry on with %s where you left off, as your instructions say.", id, id)
	if question != "" {
		msg += fmt.Sprintf(" Your question %s is answered: read the answer with bd show %s.", question, question)
	}
	if back {
		msg = fmt.Sprintf("Orchestra: your worker stopped, its tab gone, and this is your session resumed: "+
			"%s is yours again.", id)
		if question != "" {
			msg += fmt.Sprintf(" Your question %s is answered: read the answer with bd show %s.", question, question)
		}
		msg += fmt.Sprintf(" Claim %s again with bd update %s --claim, "+
			"and carry on with it where you left off, as your instructions say.", id, id)
	}
	launch := ""
	if c.LaunchPrompt {
		launch = msg // one line: Herdr passes it as it is
	}
	agent := o.agentName(id)
	begun := time.Now() // its hooks report from its start: the earlier worker's last tool use is removed
	args := workerArgs{mcp: mcpArgs, rules: rules, report: report}
	launched, stop := o.startWorker(ctx, t, agent, wt, args, launch, s.ID)
	if stop != nil {
		return stop
	}
	defer o.status(Status{Ticket: id, Gone: true})
	if !o.promptTaken(ctx, id, agent, msg, launched.atLaunch) {
		if ctx.Err() != nil {
			return errInterrupted
		}
		o.appendNotes(keep, id, fmt.Sprintf(
			"Orchestra: the worker resumed in Herdr tab %s never took up carrying on (worktree %s).", launched.tab, wt))
		return halt(ExitStuck, stopPaused, ": %s's worker, resumed in tab %s, never took up carrying on "+
			"(worktree %s); stopping so it can be looked at", id, launched.tab, wt)
	}
	o.info("  %s's worker carries on in tab %s, its session resumed; adopting it", id, launched.tab)
	w := askedWorker{tab: launched.tab, pane: launched.pane, wt: wt, hooks: launched.hooks}
	return o.adopt(ctx, t, agent, w, StateWorking, begun, how)
}
