package dispatch

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// waitSettled waits until the worker w settles, and returns when it went idle for good. Never answer
// its prompts; stop if it stays blocked for 4 minutes, or in a status the terminal can't tell (unknown)
// for 5 (see stuck). An idle worker has settled only once idleSettled says so: the terminal takes a
// worker for idle while it starts up, and while it waits on its own background command. begun is when
// it was confirmed started on its prompt. A worker the terminal has no agent under its name for is
// looked for in its pane, and named again should it have lost the name there (see workerState); one
// not there either has settled, gone. A status the terminal fails to read says nothing about the
// worker, so the wait goes on through maxFailedReads of them in a row before the run stops; so does one
// of its ticket's that bd fails to read while the worker is idle, which neither settles it nor tells it
// to continue. A worker going on too long stops the run, or is reported (see overTime). Each status
// read goes to report (nil: none), for the dashboard; what its hooks reported is read once a poll
// (hooksOf), for every decision the poll makes. A worker whose turn ends with its ticket still in
// progress is told to continue, up to maxNudges times, before its idle grace runs (see nudge); one
// whose turn ends with its ticket in progress but waiting on a question it asked has settled at that
// turn's end (see endOfTurn): its Stop hook, its StopFailure for an API error other than the usage
// limit, or Claude Code noting it idle after a permission prompt refused.
// A worker whose turn ended on the account's usage limit (limited) is none of these: the run holds for
// the limit, and the worker waits until it is told to carry on, or stops the run, as the hold says (see
// limitPoll); the time it waits doesn't count toward its ticket limit. One that closed its ticket first
// settles as usual, and a ticket it deferred as it stopped is brought back (see limitTicket).
func (o *Loop) waitSettled(
	ctx context.Context, w worker, begun time.Time, report func(ctx context.Context, st AgentState, err error),
) (idleAt time.Time, stop *stopReason) {
	var idle idleWait
	var blocked, unknown streak
	var limit limitWait
	defer func() { o.limitDone(w.id, &limit, idleAt, stop) }()
	failed := 0
	warned := false
	for polled := false; ; polled = true {
		if polled && !sleep(ctx, o.pollEvery()) || ctx.Err() != nil {
			return time.Time{}, errInterrupted
		}
		st, err := o.workerState(ctx, w)
		if report != nil {
			report(ctx, st, err)
		}
		if err != nil {
			failed++
			if stop := o.unreadStatus(w, failed, err); stop != nil {
				return time.Time{}, stop
			}
			continue
		}
		failed = 0
		if st == StateGone {
			limit.leave(time.Now(), st)
			o.info("  %s settled: its worker is gone", w.id)
			if idle.since.IsZero() {
				return time.Now(), nil
			}
			return idle.since, nil
		}
		h := o.hooksOf(w)
		st, h = o.limitTicket(ctx, w, st, h, &limit)
		if stop, told := o.limitPoll(ctx, w, st, h, &limit, carryOnAfterLimit(w.id)); stop != nil {
			return time.Time{}, stop
		} else if told {
			continue // the checks below wait for its next poll
		}
		// A permission prompt the terminal shows as idle waits for the maintainer as a blocked worker does.
		asks := asksPermission(h, st)
		if stop := stuck(w, st, asks, &blocked, &unknown); stop != nil {
			return time.Time{}, stop
		}
		isIdle := (st == StateIdle || st == StateDone) && !asks
		idle.track(isIdle)
		if isIdle {
			idleAt, stop, nudged := o.idlePoll(ctx, w, h, begun, &idle)
			if stop != nil || !idleAt.IsZero() {
				return idleAt, stop
			}
			if nudged {
				continue // the checks below wait for its next poll
			}
		}
		if stop := o.overTime(ctx, w, st, limit.limited(time.Now()), &warned); stop != nil {
			return time.Time{}, stop
		}
	}
}

// workerState reads the status of the worker w by its name, looking for it in its pane should the
// terminal have no agent under the name (see nameLost).
func (o *Loop) workerState(ctx context.Context, w worker) (AgentState, error) {
	st, err := o.agents.Status(ctx, w.agent)
	if err != nil || st != StateGone {
		return st, err
	}
	return o.nameLost(ctx, w)
}

// nameLost looks in its pane for the worker w, which the terminal has no agent under its name for: a
// program the worker runs can take the name from it while it works, as a Claude Code started there with
// the pane's Herdr variables does, Herdr's hook taking that one for the pane's agent. Found there
// unnamed (see unnamedInPane), it is named again and its state returned; StateGone otherwise, and when
// the terminal refuses the name, which is logged. The error is the terminal not saying what is there.
func (o *Loop) nameLost(ctx context.Context, w worker) (AgentState, error) {
	if w.pane == "" || o.namer == nil {
		return StateGone, nil
	}
	st, err := o.unnamedInPane(ctx, w.id, w.tab, w.pane)
	if err != nil || st == StateGone {
		return st, err
	}
	if err := o.namer.RenameAgent(ctx, w.pane, w.agent); err != nil {
		if ctx.Err() != nil {
			return "", err // cut short, not refused
		}
		o.log.Raw("", fmt.Errorf("%s's worker lost its name in tab %s and can't be named %s again, so it is taken "+
			"for gone: %w", w.id, w.tab, w.agent, err))
		return StateGone, nil
	}
	o.info("  %s's worker lost its name in tab %s; named it %s again", w.id, w.tab, w.agent)
	return st, nil
}

// unreadStatus is waitSettled's turn on the worker w's status that the terminal failed to read, the
// failed-th time in a row, with err: the first is logged, and maxFailedReads of them stop the wait.
func (o *Loop) unreadStatus(w worker, failed int, err error) *stopReason {
	if failed == 1 {
		o.log.Raw("", fmt.Errorf("cannot read the status of %s's worker; still waiting on it: %w", w.id, err))
	}
	if failed < maxFailedReads {
		return nil
	}
	return halt(ExitTool, stopTerminalFailed,
		": the status of %s's worker (tab %s) could not be read %d times in a row: %v",
		w.id, w.tab, failed, err).causedBy(err)
}

// stuck says why the wait on the worker w, in state st, stops for the maintainer: blocked, or waiting
// on a permission prompt (asks), for longer than blockedLimit, or in a status the terminal can't tell
// for longer than unknownLimit; nil while neither. blocked and unknown are those streaks, kept from one
// poll to the next.
func stuck(w worker, st AgentState, asks bool, blocked, unknown *streak) *stopReason {
	if blocked.track(st == StateBlocked || asks) > blockedLimit {
		if asks {
			return halt(ExitStuck, stopBlocked,
				" >4min: tab %s (%s) needs attention: its worker waits on a permission prompt", w.tab, w.id)
		}
		return halt(ExitStuck, stopBlocked, " >4min: tab %s (%s) needs attention", w.tab, w.id)
	}
	if unknown.track(st == StateUnknown) > unknownLimit {
		return halt(ExitStuck, stopUnknown,
			" >5min: the terminal can't tell what the worker in tab %s (%s) is doing; it needs attention", w.tab, w.id)
	}
	return nil
}

// overTime says why the wait on the worker w, in state st, stops for how long it has gone on: past
// Config.TicketLimit since it started (dispatch), noted on its ticket. Without a limit, one still going
// after longRunning is reported once, warned noting it was; nil while it may go on. The time it waited
// on the usage limit, limited, doesn't count.
func (o *Loop) overTime(ctx context.Context, w worker, st AgentState, limited time.Duration,
	warned *bool) *stopReason {
	gone := time.Since(w.started) - limited
	if limit := o.cfg.TicketLimit; limit > 0 && gone > limit {
		o.appendNotes(context.WithoutCancel(ctx), w.id, fmt.Sprintf(
			"Orchestra: worker in tab %s was still %s after the %s ticket limit (worktree %s).",
			w.tab, st, command.ShortDuration(limit), w.wt))
		return halt(ExitStuck, stopTicketLimit,
			": %s still %s after %s in tab %s (worktree %s); stopping so it can be looked at",
			w.id, st, command.ShortDuration(limit), w.tab, w.wt)
	}
	if o.cfg.TicketLimit == 0 && !*warned && gone > longRunning {
		*warned = true
		o.emit(Event{Kind: EvWarn, Ticket: w.id, Text: fmt.Sprintf(
			"  LONG_RUNNING: %s still %s after %s in tab %s; still waiting on it, as no ticket limit is set (--ticket-limit)",
			w.id, st, command.ShortDuration(longRunning), w.tab)})
	}
	return nil
}

// hooksSeen is what a worker's hooks reported, as read once at a poll: use, the last tool use, if ok
// (it reported one, since it was started or adopted), and rec, the rest of their record. use is kept
// when it is of a turn before the worker was adopted, for its time only. The zero hooksSeen is a
// worker that doesn't report through hooks.
type hooksSeen struct {
	use ToolUse
	ok  bool
	rec HookRecord
}

// hooksOf reads what the hooks of the worker w reported; nothing for a worker without them.
func (o *Loop) hooksOf(w worker) hooksSeen {
	if !w.hooks || o.reporter == nil {
		return hooksSeen{}
	}
	u, ok := o.reporter.LastToolUse(w.wt)
	if ok && !w.since.IsZero() && (u.At.IsZero() || u.At.Before(w.since)) {
		ok = false // of a turn before it was adopted
	}
	return hooksSeen{use: u, ok: ok, rec: o.reporter.HookRecord(w.wt)}
}

// subagentRunning says whether the hooks tell a subagent still running at now: one started and not
// stopped, while hooks keep reporting (HookRecord.SubagentsRunning).
func (h hooksSeen) subagentRunning(now time.Time) bool {
	return h.rec.SubagentsRunning(h.use.At, now)
}

// asksPermission says whether a worker in state st as the terminal shows it, whose hooks reported h,
// waits on a permission prompt.
func asksPermission(h hooksSeen, st AgentState) bool {
	return waitsOnPermission(st, h.use, h.ok)
}

// streak is how long a condition has held without a break, from one poll to the next.
type streak struct {
	since time.Time // when it began to hold (zero: it doesn't)
}

// track notes whether the condition holds at this poll, and returns how long it has held (zero: it
// doesn't).
func (s *streak) track(on bool) time.Duration {
	if !on {
		s.since = time.Time{}
		return 0
	}
	if s.since.IsZero() {
		s.since = time.Now()
	}
	return time.Since(s.since)
}

// idleWait is what waitSettled keeps of a worker's idle polls from one to the next.
type idleWait struct {
	streak           // how long it has been idle
	readTo time.Time // turns that ended before it have been read; the next turn's end comes after
	unread int       // failed reads of its ticket's status in a row
	nudges int       // times it was told to continue
}

// idlePoll is a poll of waitSettled's that finds the worker w idle, as it has been since idle.since,
// its hooks having reported h; begun is when it was confirmed started on its prompt. It reads w's
// ticket and returns when w went idle once it has settled: at its turn's end with its ticket waiting
// on a question (endOfTurn), or as idleSettled says. A worker whose turn ended with its ticket still in
// progress is told to continue first, up to maxNudges times; one that takes the message up (nudged)
// starts its idle time afresh.
// stop is why the wait stops on it; a zero idleAt and a nil stop say it goes on.
func (o *Loop) idlePoll(ctx context.Context, w worker, h hooksSeen, begun time.Time, idle *idleWait) (
	idleAt time.Time, stop *stopReason, nudged bool) {
	ts, err := o.tickets.Status(ctx, w.id)
	if ctx.Err() != nil {
		return time.Time{}, errInterrupted, false // the status read was cut short, and says nothing
	}
	if err != nil {
		if idle.unread++; idle.unread == 1 {
			o.log.Raw("", fmt.Errorf("cannot read the status of %s; still waiting on its worker: %w", w.id, err))
		}
		if idle.unread < maxFailedReads {
			return time.Time{}, nil, false
		}
		return time.Time{}, halt(ExitTool, stopStatusUnreadable,
			" for %s: its status could not be read %d times in a row while its worker was idle%s; "+
				"stopping rather than guessing (worktree %s and tab %s left open)",
			w.id, idle.unread, because(err), w.wt, w.tab).causedBy(err), false
	}
	idle.unread = 0
	var turn turnEnd
	if ts == StatusInProgress {
		turn = o.endOfTurn(ctx, w.id, h, later(w.since, idle.readTo))
	}
	if turn.asked != nil { // conclude reopens the ticket left in progress
		o.info("  %s settled: %s, waiting on %s", w.id, turnEndName(h, turn.at), turn.asked.ID)
		return idle.since, nil, false
	}
	if turn.owes {
		idle.readTo = time.Now() // told to continue or not, only its next turn's end counts
	}
	if turn.owes && idle.nudges < maxNudges {
		idle.nudges++
		if o.nudge(ctx, w.id, w.agent, idle.nudges) {
			idle.streak = streak{}
			return time.Time{}, nil, true
		}
		if ctx.Err() != nil {
			return time.Time{}, errInterrupted, false
		}
	}
	now := time.Now()
	settled, why := idleSettled(ts, h, w.since, now, quietFor(h, idle.since, now), time.Since(begun))
	if !settled {
		return time.Time{}, nil, false
	}
	o.info("  %s settled: %s", w.id, why)
	return idle.since, nil, false
}

// quietFor is how long a worker whose hooks reported h, which the terminal has shown idle since since,
// has done nothing, at now. A subagent in the background works on after its worker's turn has ended,
// and its tool uses are reported as the worker's: while one runs, the last of them ends the quiet. A
// note of its hooks ends it too: a subagent's stop, say, whose answer starts the worker's next turn,
// which the terminal may not show yet.
func quietFor(h hooksSeen, since, now time.Time) time.Duration {
	since = later(since, h.rec.Noted)
	if h.ok && h.subagentRunning(now) {
		since = later(since, h.use.At)
	}
	return now.Sub(since)
}

// blockedLimit is how long a worker may stay blocked before the run stops for it.
const blockedLimit = 4 * time.Minute

// unknownLimit is how long a worker's status may stay unknown (the terminal can't tell what it is doing)
// before the run stops for it.
const unknownLimit = 5 * time.Minute

// longRunning is how long a worker may go on, without a ticket limit, before the run reports it.
const longRunning = 2 * time.Hour

// maxFailedReads is how many failed status reads in a row (a minute's worth), of the worker's from
// the terminal or of its ticket's from bd, stop the wait on a worker.
const maxFailedReads = 20

// readStatus reads the worker's status, trying up to tries times while the terminal fails to
// answer. A failure it tries again after is logged; the last, if the terminal never answers, is
// returned for the caller to report. agent is the worker's name in the terminal.
func (o *Loop) readStatus(ctx context.Context, agent string, tries int) (AgentState, error) {
	for try := 1; ; try++ {
		st, err := o.agents.Status(ctx, agent)
		if err == nil || try == tries || !sleep(ctx, o.pollEvery()) {
			return st, err
		}
		o.log.Raw("", err)
	}
}

// idleGrace is how long an idle worker whose ticket is still in progress may take to resume
// (typically it is waiting on its own background command) before the run pauses for it, and how
// long one whose hooks last reported a tool use may stay idle before its turn counts as over.
const idleGrace = 10 * time.Minute

// startGrace is how long after it started on its prompt an idle worker may leave its ticket open
// (still starting up, or not yet at bd update --claim) before it counts as settled, when no hook
// says whether its turn is over.
const startGrace = 3 * time.Minute

// maxNudges is how many times a worker whose turn ends with its ticket still in progress is told to
// continue before the idle grace and the pause apply as to any other.
const maxNudges = 2

// turnEnd is what a worker's ticket shows at the end of its turn: the work still owed, or a
// question asked. A zero turnEnd says neither, as when the turn hasn't ended.
type turnEnd struct {
	at    time.Time // of its end (see turnEnded)
	owes  bool      // the ticket is in progress, neither waiting on a question nor deferred
	asked *Ticket   // the open question the ticket waits on, whatever its status
}

// endOfTurn reads ticket id as its worker, whose hooks reported h, left it at the end of a turn, after
// after (zero: any). Without hooks, before that turn's end (turnEnded), or when bd fails to show the
// ticket, it says nothing: the worker neither settles nor is told to continue on it.
func (o *Loop) endOfTurn(ctx context.Context, id string, h hooksSeen, after time.Time) turnEnd {
	at, ended, _ := turnEnded(h, time.Now())
	if !ended || !after.IsZero() && (at.IsZero() || at.Before(after)) {
		return turnEnd{}
	}
	t, err := o.tickets.Show(ctx, id)
	if err != nil {
		return turnEnd{}
	}
	q := OpenQuestion(t)
	return turnEnd{at: at, owes: t.Status == StatusInProgress && q == nil, asked: q}
}

// turnEnded says whether the turn of a worker whose hooks reported h has ended, at now, and when: at
// its Stop hook, which the last tool use is or which a PreToolUse record written after it replaced
// (replaced), or, after a permission prompt reported, when Claude Code noted it idle at its prompt
// (ToolUse.Idle): the prompt was refused, which ends the turn with no Stop hook. An API error (a 500,
// a 529, a rate limit that isn't the account's) ends the turn with a StopFailure hook instead of the
// Stop, which counts as one; one that ended on the account's usage limit (ToolUse.Limit) doesn't: the
// run holds for it (see limitPoll), and the worker waits until it is told to carry on. Nor does a Stop
// that is still ending (ToolUse.Ending): whether the turn ended on the limit isn't told yet. Without a tool
// use reported, it hasn't. Claude Code forks agents at the end of a turn (a prompt suggestion, say)
// that run the worker's PreToolUse hook and are then refused the tool, so no PostToolUse follows; a prompt
// that starts a turn (a nudge, an answer) clears the Stop. While a subagent runs the turn goes on,
// Stop or not: one in the background (as every subagent is in fork mode) outlives the turn that
// started it, and its answer starts the next, and one still running after the Stop may be the one
// using the tool. One that stopped after the Stop (see answerPending) has an answer on its way, which
// starts the next turn before the terminal may show it.
func turnEnded(h hooksSeen, now time.Time) (at time.Time, ended, replaced bool) {
	u := h.use
	switch {
	case !h.ok, u.Limit.Reached(), u.Ending:
		return time.Time{}, false, false
	case u.Event == "Stop", u.Event == "StopFailure":
		at = u.At
	case u.Event == "PreToolUse" && !u.Stopped.IsZero() && !u.At.IsZero() && !u.At.Before(u.Stopped):
		at, replaced = u.Stopped, true
	case u.Event == EventPermission && !u.Idle.IsZero():
		at = u.Idle
	default:
		return time.Time{}, false, false
	}
	if h.subagentRunning(now) || answerPending(h.rec, at) {
		return time.Time{}, false, false
	}
	return at, true, replaced
}

// turnEndName names, for the log, the end of the turn of a worker whose hooks reported h, which
// turnEnded found ended at at.
func turnEndName(h hooksSeen, at time.Time) string {
	u := h.use
	switch {
	case u.Event == EventPermission:
		return fmt.Sprintf("idle at its prompt at %s after a permission prompt for %s refused",
			at.Format("15:04:05"), u.Tool)
	case u.Event == "StopFailure", u.Failure != "": // or one a PreToolUse record replaced
		name := "StopFailure hook"
		if u.Failure != "" {
			name += " (" + u.Failure + ")"
		}
		return name + " at " + at.Format("15:04:05")
	}
	return "Stop hook at " + at.Format("15:04:05")
}

// answerPending says whether a subagent of the worker whose hooks noted rec stopped after its Stop
// hook at stop (zero: of unknown time, which nothing counts as after), so that its answer starts the
// worker's next turn. Only a subagent that started counts: Claude Code's forks at the end of a turn
// note a stop of their own, a moment after the Stop.
func answerPending(rec HookRecord, stop time.Time) bool {
	return !stop.IsZero() && rec.SubagentStopped.After(stop)
}

// nudge tells ticket id's worker, idle at the end of its turn, that its ticket is still open and
// to go on; n counts the times. It reports whether the worker took the message up.
func (o *Loop) nudge(ctx context.Context, id, agent string, n int) bool {
	msg := fmt.Sprintf("Orchestra: your ticket %s is still in progress. Continue; if something blocks you, "+
		"ask or defer as your instructions say.", id)
	if !o.deliverPrompt(ctx, agent, msg) {
		if ctx.Err() == nil {
			o.log.Raw("", fmt.Errorf("%s's worker ended its turn with the ticket in progress and did not take "+
				"the message to continue", id))
		}
		return false
	}
	o.info("  %s's worker ended its turn with the ticket still in progress; told it to continue (%d of %d)",
		id, n, maxNudges)
	return true
}

// later is the later of two times.
func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// idleSettled decides whether a worker the terminal shows idle has settled, at now, from its ticket's
// status, what its hooks reported (h), how long it has been idle (idleFor) and how long since it started
// on its prompt (running); why names what decided it. since is when it was adopted (zero: it wasn't).
// Case by case, as it returns:
//   - A ticket in progress: settled after idleGrace, as its worker may wait on a background command.
//   - With hooks, a report before since, or of unknown time with since set, is of a turn before the
//     worker was adopted, and doesn't count (hooksOf).
//   - The rest as hooksSettled says.
//   - No report, or no hooks, and an open ticket: settled startGrace after the worker started.
//   - Any other status: settled, as the worker is done with its ticket.
func idleSettled(ticket TicketStatus, h hooksSeen, since, now time.Time,
	idleFor, running time.Duration) (settled bool, why string) {
	switch {
	case ticket == StatusInProgress:
		return idleFor >= idleGrace,
			fmt.Sprintf("idle for %s with the ticket still in progress", command.ShortDuration(idleGrace))
	case h.ok:
		return hooksSettled(h, since, now, idleFor)
	case ticket == StatusOpen:
		return running >= startGrace, fmt.Sprintf("idle %s after it started, with the ticket still open",
			command.ShortDuration(startGrace))
	}
	return true, "idle with the ticket " + string(ticket)
}

// hooksSettled is idleSettled for a worker whose ticket isn't in progress and whose hooks reported a
// tool use (h.ok). Case by case, as it returns:
//   - A Stop hook, a StopFailure for an API error other than the usage limit, a PreToolUse record of
//     no turn that replaced either, or a permission prompt refused (see turnEnded), not before since:
//     settled, as the record is removed before a worker starts, so any turn's end came after its prompt.
//   - A subagent still running: the turn goes on; settled after idleGrace.
//   - A subagent that stopped past the Stop, with no turn on its answer yet: settled after idleGrace.
//   - A turn that ended on the usage limit, which the terminal shows idle rather than limited: settled
//     after idleGrace, as the run holds for the limit only while the terminal shows it (see limitPoll).
//   - A Stop still ending (ToolUse.Ending), as for a moment after it: settled after idleGrace.
//   - Any other last report, a tool use: mid-turn, whatever the terminal says; settled after
//     idleGrace, as a turn interrupted ends with no hook.
func hooksSettled(h hooksSeen, since, now time.Time, idleFor time.Duration) (settled bool, why string) {
	u := h.use
	if at, ended, replaced := turnEnded(h, now); ended && (since.IsZero() || !at.Before(since)) {
		why := turnEndName(h, at)
		if replaced {
			why += "; then a PreToolUse record with no turn of its own, " + u.Reporter()
		}
		return true, why
	}
	if h.subagentRunning(now) {
		return idleFor >= idleGrace, fmt.Sprintf("idle for %s after a %s hook, with a subagent still running",
			command.ShortDuration(idleGrace), u.Event)
	}
	stop, hook := u.Stopped, "Stop"
	switch {
	case u.Event == "Stop", u.Event == "StopFailure":
		stop, hook = u.At, u.Event
	case u.Failure != "": // a StopFailure a later record replaced
		hook = "StopFailure"
	}
	if answerPending(h.rec, stop) {
		return idleFor >= idleGrace, fmt.Sprintf("idle for %s after a subagent stopped at %s, past the "+
			"turn's %s hook, with no turn on its answer", command.ShortDuration(idleGrace),
			h.rec.SubagentStopped.Format("15:04:05"), hook)
	}
	if u.Limit.Reached() {
		return idleFor >= idleGrace, fmt.Sprintf("idle for %s after its turn ended on the usage limit at %s (%s)",
			command.ShortDuration(idleGrace), u.Limit.At.Format("15:04:05"), u.Limit.Message)
	}
	if u.Ending {
		return idleFor >= idleGrace, fmt.Sprintf("idle for %s after a Stop hook at %s, the turn's end not yet logged",
			command.ShortDuration(idleGrace), u.Stopped.Format("15:04:05"))
	}
	return idleFor >= idleGrace, fmt.Sprintf("idle for %s after a %s hook, with no Stop hook; that record was %s",
		command.ShortDuration(idleGrace), u.Event, u.Reporter())
}

// watcher shows a worker's status and latest action on the dashboard, and logs what its hooks
// note: a permission prompt it waits on, a compaction of its context.
type watcher struct {
	o       *Loop
	wt      string
	id, tab string      // its ticket, and the tab its worker is in
	noted   *HookRecord // the hooks' record as last read; nil before the first read, which logs nothing
	asked   time.Time   // when the permission prompt last logged was reported
}

// newWatcher is a watcher of the worker in worktree wt on ticket st.Ticket, in tab st.Tab. What it reads
// changes the ticket's status as the loop keeps it (see updateActive).
func (o *Loop) newWatcher(wt string, st Status) *watcher {
	return &watcher{o: o, wt: wt, id: st.Ticket, tab: st.Tab}
}

// report shows the worker in state st, as just read, reading its screen for its latest action; err
// is why the state could not be read. The latest action read is kept while the screen can't be.
func (w *watcher) report(ctx context.Context, st AgentState, err error) {
	o := w.o
	read := err == nil && st != StateGone
	var activity string
	if read {
		activity = lastActivity(o.agents.Screen(ctx, o.agentName(w.id), st))
	}
	var doing Doing
	permission := false
	if o.reporter != nil && read {
		u, ok := o.reporter.LastToolUse(w.wt)
		rec := o.reporter.HookRecord(w.wt)
		switch {
		case st == StateWorking:
			if ok {
				doing = DoingOf(u, o.cfg.Check)
			}
			if doing == "" && rec.SubagentsRunning(u.At, time.Now()) {
				doing = DoingSubagent
			}
		case waitsOnPermission(st, u, ok):
			permission = true
			w.notePermission(u)
		}
		w.noteCompactions(rec)
	}
	o.updateActive(w.id, func(s *Status) {
		s.Agent, s.Unreadable, s.Doing, s.Permission = st, err != nil, doing, permission
		if read {
			s.Activity = activity
		}
	})
}

// notePermission logs the permission prompt the worker reported in u, once.
func (w *watcher) notePermission(u ToolUse) {
	if !u.At.IsZero() && u.At.Equal(w.asked) {
		return
	}
	w.asked = u.At
	what := u.Tool
	if rm := GuardedRemoval(u.Command); rm != "" {
		// The command's start may not show the removal, and allowing the tool would not spare this wait.
		what += " (" + rm + "), a removal Claude Code's built-in safety check holds for a person, which " +
			"no allow rule skips,"
	} else if cmd := Clip(u.Command, 80); cmd != "" {
		what += " (" + cmd + ")"
	}
	w.o.emit(Event{Kind: EvWarn, Ticket: w.id, Text: fmt.Sprintf(
		"  PERMISSION_PROMPT: %s's worker waits on a permission prompt for %s in tab %s; allow or deny it there",
		w.id, what, w.tab)})
}

// noteCompactions logs the compactions of the worker's context in rec that the last read hadn't.
func (w *watcher) noteCompactions(rec HookRecord) {
	if w.noted != nil {
		for _, trigger := range rec.Compactions[min(len(w.noted.Compactions), len(rec.Compactions)):] {
			w.o.info("  %s's worker's context was compacted (%s)", w.id, trigger)
		}
	}
	w.noted = &rec
}

// watch reads the worker's status every poll and reports it until the returned stop function is
// called; stop waits for the last report, so no update lands after it. The settle loop reports the
// status it reads itself, so the watcher is stopped before it starts: each poll reads the status
// once.
func (o *Loop) watch(ctx context.Context, w *watcher) (stop func()) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	agent := o.agentName(w.id)
	go func() {
		defer close(done)
		defer func() {
			if p := recover(); p != nil { // the worker goes on; only its status stops showing until it settles
				o.emit(Event{Kind: EvWarn, Ticket: w.id, Text: fmt.Sprintf("  WATCH_FAILED for %s: panic: %s; "+
					"its status is not shown until it takes its prompt (the stack is in %s)",
					w.id, o.logPanic("watching "+w.id, p), o.cfg.LogPath)})
			}
		}()
		tick := time.NewTicker(o.pollEvery())
		defer tick.Stop()
		for {
			st, err := o.agents.Status(ctx, agent) // a failure shows as unreadable; the start logs its own
			if ctx.Err() != nil {
				return
			}
			w.report(ctx, st, err)
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { cancel(); <-done }) }
}
