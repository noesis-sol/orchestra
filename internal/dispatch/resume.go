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
	if !hooks || !o.hooksRecorded() {
		return Session{}, false
	}
	return o.reporter.Session(wt)
}

// claimSessionResume reports whether ticket id's worker has not had its session resumed in this run yet,
// and notes that it has from now on.
func (o *Loop) claimSessionResume(id string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.sessionResumed[id] {
		return false
	}
	o.sessionResumed[id] = true
	return true
}

// workerGone reports whether the terminal has no worker w, under its name or in its pane (see
// workerState); a status it can't read says nothing.
func (o *Loop) workerGone(ctx context.Context, w worker) bool {
	st, err := o.workerState(ctx, w)
	return err == nil && st == StateGone
}

// pausedGone is conclude's outcome for the ticket, in progress with its worker w gone from its tab:
// its session resumed in a new tab, the first time in this run, or else the run stopped (PAUSED).
func (r *ticketRun) pausedGone(ctx context.Context, w worker) *stopReason {
	o, id := r.o, r.id
	s, ok := o.sessionOf(w.wt, w.hooks)
	if ok && o.claimSessionResume(id) {
		return r.resumeSession(ctx, w.wt, w.tab, "", goneMidTicket, s)
	}
	o.noteWorkerGone(ctx, id, w.tab, w.wt, "")
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
func (r *runState) resumedInTab(t Ticket, w workerSpot, s Session) adoption {
	r.o.book.answer(t.ID)
	r.o.setParent(t.ID, t.Parent)
	r.startFootprint(t)
	return adoption{t: t, w: w, resume: &s}
}

// resumeWhy is why a worker has its session resumed (see resumeSession).
type resumeWhy int

const (
	goneMidTicket    resumeWhy = iota // its worker gone from its tab with the ticket in progress
	backFromQuestion                  // the ticket back through bd ready, open, for it to claim again
)

// resumeTexts is what is said as a worker has its session resumed: the note on its ticket, the
// log's line and what the worker is told.
type resumeTexts struct {
	note, text, msg string
}

// texts is what is said as ticket id's worker, gone from tab, has its session sid resumed in a new
// tab in worktree wt for why; question is the one it was left waiting on, if any, answered since.
func (why resumeWhy) texts(id, tab, wt, question, sid string) resumeTexts {
	answered := ""
	if question != "" {
		answered = fmt.Sprintf(" Your question %s is answered: read the answer with bd show %s.", question, question)
	}
	if why == backFromQuestion {
		return resumeTexts{
			note: fmt.Sprintf("Orchestra: the ticket is back, and its worker in tab %s is gone (worktree %s); "+
				"its session %s is resumed in a new tab.", tab, wt, sid),
			text: fmt.Sprintf("  RESUMED: %s is back, and its worker is gone from tab %s; "+
				"resuming its session %s in a new tab (worktree %s)", id, tab, sid, wt),
			msg: fmt.Sprintf("Orchestra: your worker stopped, its tab gone, and this is your session resumed: "+
				"%s is yours again.", id) + answered + fmt.Sprintf(" Claim %s again with bd update %s --claim, "+
				"and carry on with it where you left off, as your instructions say.", id, id),
		}
	}
	return resumeTexts{
		note: workerGoneNote(tab, wt, "") + fmt.Sprintf("; its session %s is resumed in a new tab.", sid),
		text: fmt.Sprintf("  RESUMED: %s's worker is gone from tab %s with the ticket in progress; "+
			"resuming its session %s in a new tab (worktree %s)", id, tab, sid, wt),
		msg: fmt.Sprintf("Orchestra: your worker stopped, its tab gone, with %s still in progress, "+
			"and this is your session resumed. Carry on with %s where you left off, as your instructions say.",
			id, id) + answered,
	}
}

// resumeSession starts the ticket's worker again, gone from tab, for why, in a new tab in its
// worktree wt, resuming its session s, and tells it to carry on; question is the one it was left
// waiting on, if any, answered since. The worker is then adopted, as one back from a question is
// (see adopt), its ticket limit counted afresh. It returns as work does.
func (r *ticketRun) resumeSession(ctx context.Context, wt, tab, question string, why resumeWhy,
	s Session) *stopReason {
	o, c, t, id, keep := r.o, r.o.cfg, r.t, r.id, r.keep
	said := why.texts(id, tab, wt, question, s.ID)
	o.appendNotes(keep, id, said.note)
	o.emit(Event{Kind: EvWarn, Ticket: id, Title: t.Title, Detail: "its worker gone, its session resumed",
		Text: said.text})

	ws, done, stop := r.setup(ctx, wt, true)
	if done {
		return stop
	}

	msg := said.msg
	if spec := specNote(t, "carry on"); spec != "" {
		msg += " " + spec // still one line, for Herdr to pass at launch
	}
	launch := ""
	if c.LaunchPrompt {
		launch = msg // one line: Herdr passes it as it is
	}
	agent := r.agent
	begun := time.Now() // its hooks report from its start: the earlier worker's last tool use is removed
	launched, stop := o.startWorker(ctx, t, agent, wt, ws.args, launch, s.ID)
	if stop != nil {
		return stop
	}
	defer o.status(Status{Ticket: id, Gone: true})
	if !o.promptTaken(ctx, id, agent, msg, launched.atLaunch, true) {
		if ctx.Err() != nil {
			return errInterrupted
		}
		o.appendNotes(keep, id, fmt.Sprintf(
			"Orchestra: the worker resumed in tab %s never took up carrying on (worktree %s).", launched.tab, wt))
		return halt(ExitStuck, stopPaused, ": %s's worker, resumed in tab %s, never took up carrying on "+
			"(worktree %s); stopping so it can be looked at", id, launched.tab, wt)
	}
	o.info("  %s's worker carries on in tab %s, its session resumed; adopting it", id, launched.tab)
	w := workerSpot{tab: launched.tab, pane: launched.pane, wt: wt, hooks: launched.hooks}
	return r.adopt(ctx, w, StateWorking, begun)
}
