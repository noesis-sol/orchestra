package dispatch

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// waitSettled waits until the worker w settles, and returns when it went idle for good. Never answer
// its prompts; stop if it stays blocked for 4 minutes, or in a status Herdr can't tell (unknown) for
// 5. An idle worker has settled only once idleSettled says so: Herdr takes a worker for idle while
// it starts up, and while it waits on its own background command. begun is when it was confirmed
// started on its prompt. A status Herdr fails to read says nothing about the worker, so the wait
// goes on through maxFailedReads of them in a row before the run stops; so does one of its ticket's
// that bd fails to read while the worker is idle, which neither settles it nor tells it to continue.
// A worker still going Config.TicketLimit after it started (dispatch) stops the run; without a
// limit, one still going after longRunning is reported once. Each status read goes to report (nil:
// none), for the dashboard. A worker whose turn ends with its ticket still in progress is told to
// continue, up to maxNudges times, before its idle grace runs (see nudge); one whose turn ends with
// its ticket in progress but waiting on a question it asked has settled at that Stop hook (see
// endOfTurn).
func (o *Loop) waitSettled(
	ctx context.Context, w worker, begun time.Time, report func(ctx context.Context, st AgentState, err error),
) (idleAt time.Time, stop *stopReason) {
	id, agent, tab, wt := w.id, w.agent, w.tab, w.wt
	var idle idleWait
	var blocked, unknown streak
	failed := 0
	warned := false
	for polled := false; ; polled = true {
		if polled && !sleep(ctx, o.pollEvery()) || ctx.Err() != nil {
			return time.Time{}, errInterrupted
		}
		st, err := o.agents.Status(ctx, agent)
		if report != nil {
			report(ctx, st, err)
		}
		if err != nil {
			if failed++; failed == 1 {
				o.log.Raw("", fmt.Errorf("cannot read the status of %s's worker; still waiting on it: %w", id, err))
			}
			if failed >= maxFailedReads {
				return time.Time{}, halt(ExitTool, stopHerdrFailed,
					": the status of %s's worker (tab %s) could not be read %d times in a row: %v",
					id, tab, failed, err).causedBy(err)
			}
			continue
		}
		failed = 0
		if st == StateGone {
			o.info("  %s settled: its worker is gone", id)
			if idle.since.IsZero() {
				return time.Now(), nil
			}
			return idle.since, nil
		}
		// A permission prompt Herdr shows as idle waits for the maintainer as a blocked worker does.
		asks := err == nil && o.asksPermission(w, st)
		if blocked.track(st == StateBlocked || asks) > blockedLimit {
			if asks {
				return time.Time{}, halt(ExitStuck, stopBlocked,
					" >4min: tab %s (%s) needs attention: its worker waits on a permission prompt", tab, id)
			}
			return time.Time{}, halt(ExitStuck, stopBlocked, " >4min: tab %s (%s) needs attention", tab, id)
		}
		if unknown.track(st == StateUnknown) > unknownLimit {
			return time.Time{}, halt(ExitStuck, stopUnknown,
				" >5min: Herdr can't tell what the worker in tab %s (%s) is doing; it needs attention", tab, id)
		}
		isIdle := (st == StateIdle || st == StateDone) && !asks
		idle.track(isIdle)
		if isIdle {
			idleAt, stop, nudged := o.idlePoll(ctx, w, begun, &idle)
			if stop != nil || !idleAt.IsZero() {
				return idleAt, stop
			}
			if nudged {
				continue // the checks below wait for its next poll
			}
		}
		if limit := o.cfg.TicketLimit; limit > 0 && time.Since(w.started) > limit {
			o.appendNotes(context.WithoutCancel(ctx), id, fmt.Sprintf(
				"Orchestra: worker in Herdr tab %s was still %s after the %s ticket limit (worktree %s).",
				tab, st, command.ShortDuration(limit), wt))
			return time.Time{}, halt(ExitStuck, stopTicketLimit,
				": %s still %s after %s in tab %s (worktree %s); stopping so it can be looked at",
				id, st, command.ShortDuration(limit), tab, wt)
		}
		if o.cfg.TicketLimit == 0 && !warned && time.Since(w.started) > longRunning {
			warned = true
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  LONG_RUNNING: %s still %s after %s in tab %s; still waiting on it, as no ticket limit is set (--ticket-limit)",
				id, st, command.ShortDuration(longRunning), tab)})
		}
	}
}

// asksPermission says whether the worker w, in state st as Herdr shows it, waits on a permission
// prompt, as its hooks reported since it was started or adopted.
func (o *Loop) asksPermission(w worker, st AgentState) bool {
	if !w.hooks || o.reporter == nil {
		return false
	}
	u, ok := o.reporter.LastToolUse(w.wt)
	if ok && !w.since.IsZero() && (u.At.IsZero() || u.At.Before(w.since)) {
		ok = false // of a turn before it was adopted
	}
	return waitsOnPermission(st, u, ok)
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
	readTo time.Time // turns that ended before it have been read; the next turn's Stop comes after
	unread int       // failed reads of its ticket's status in a row
	nudges int       // times it was told to continue
}

// idlePoll is a poll of waitSettled's that finds the worker w idle, as it has been since idle.since;
// begun is when it was confirmed started on its prompt. It reads w's ticket and returns when w went
// idle once it has settled: at a Stop hook with its ticket waiting on a question (endOfTurn), or as
// idleSettled says. A worker whose turn ended with its ticket still in progress is told to continue
// first, up to maxNudges times; one that takes the message up (nudged) starts its idle time afresh.
// stop is why the wait stops on it; a zero idleAt and a nil stop say it goes on.
func (o *Loop) idlePoll(ctx context.Context, w worker, begun time.Time, idle *idleWait) (
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
		turn = o.endOfTurn(ctx, w.id, w.wt, w.hooks, later(w.since, idle.readTo))
	}
	if turn.asked != nil { // conclude reopens the ticket left in progress
		o.info("  %s settled: Stop hook at %s, waiting on %s", w.id, turn.at.Format("15:04:05"), turn.asked.ID)
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
	settled, why := o.idleSettled(ts, w.wt, w.hooks, w.since, time.Since(idle.since), time.Since(begun))
	if !settled {
		return time.Time{}, nil, false
	}
	o.info("  %s settled: %s", w.id, why)
	return idle.since, nil, false
}

// blockedLimit is how long a worker may stay blocked before the run stops for it.
const blockedLimit = 4 * time.Minute

// unknownLimit is how long a worker's status may stay unknown (Herdr can't tell what it is doing)
// before the run stops for it.
const unknownLimit = 5 * time.Minute

// longRunning is how long a worker may go on, without a ticket limit, before the run reports it.
const longRunning = 2 * time.Hour

// maxFailedReads is how many failed status reads in a row (a minute's worth), of the worker's from
// Herdr or of its ticket's from bd, stop the wait on a worker.
const maxFailedReads = 20

// readStatus reads the worker's status, trying up to tries times while Herdr fails to answer. A
// failure it tries again after is logged; the last, if Herdr never answers, is returned for the
// caller to report. agent is the worker's Herdr name.
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
	at    time.Time // of its Stop hook
	owes  bool      // the ticket is in progress, neither waiting on a question nor deferred
	asked *Ticket   // the open question the ticket waits on, whatever its status
}

// endOfTurn reads ticket id as its worker, reporting through hooks, left it at the end of a turn,
// after after (zero: any). Without hooks, before that turn's Stop, or when bd fails to show the
// ticket, it says nothing: the worker neither settles nor is told to continue on it.
func (o *Loop) endOfTurn(ctx context.Context, id, wt string, hooks bool, after time.Time) turnEnd {
	if !hooks || o.reporter == nil {
		return turnEnd{}
	}
	u, ok := o.reporter.LastToolUse(wt)
	if !ok || u.Event != "Stop" || !after.IsZero() && (u.At.IsZero() || u.At.Before(after)) {
		return turnEnd{}
	}
	t, err := o.tickets.Show(ctx, id)
	if err != nil {
		return turnEnd{}
	}
	q := OpenQuestion(t)
	return turnEnd{at: u.At, owes: t.Status == StatusInProgress && q == nil, asked: q}
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

// idleSettled decides whether a worker Herdr shows idle has settled, from its ticket's status, how
// long it has been idle (idleFor) and how long since it started on its prompt (running); why names
// what decided it. A ticket in progress gets idleGrace, as its worker may be waiting on its own
// background command. Otherwise a worker that reports through hooks has settled at its Stop hook,
// the end of its turn: the record is removed before it starts, so any Stop came after its prompt.
// An adopted worker's record is not, so a report before since (or of unknown time, with since set)
// is of an earlier turn, and doesn't count. While its last report is a tool use it is mid-turn,
// whatever Herdr says, for up to idleGrace (a turn that fails ends without a Stop). Before its first
// report, or without hooks, an open ticket gets startGrace; any other status means the worker is
// done with it.
func (o *Loop) idleSettled(ticket TicketStatus, wt string, hooks bool, since time.Time,
	idleFor, running time.Duration) (settled bool, why string) {
	if ticket == StatusInProgress {
		return idleFor >= idleGrace,
			fmt.Sprintf("idle for %s with the ticket still in progress", command.ShortDuration(idleGrace))
	}
	if hooks && o.reporter != nil {
		u, ok := o.reporter.LastToolUse(wt)
		if ok && !since.IsZero() && (u.At.IsZero() || u.At.Before(since)) {
			ok = false // of a turn before it was adopted
		}
		if ok {
			if u.Event == "Stop" {
				return true, "Stop hook at " + u.At.Format("15:04:05")
			}
			return idleFor >= idleGrace, fmt.Sprintf("idle for %s after a %s hook, with no Stop hook",
				command.ShortDuration(idleGrace), u.Event)
		}
	}
	if ticket == StatusOpen {
		return running >= startGrace, fmt.Sprintf("idle %s after it started, with the ticket still open",
			command.ShortDuration(startGrace))
	}
	return true, "idle with the ticket " + string(ticket)
}

// watcher shows a worker's status and latest action on the dashboard, and logs what its hooks
// note: a permission prompt it waits on, a compaction of its context.
type watcher struct {
	o        *Loop
	wt       string
	base     Status
	activity string      // the latest action read, kept while the screen can't be
	noted    *HookRecord // the hooks' record as last read; nil before the first read, which logs nothing
	asked    time.Time   // when the permission prompt last logged was reported
}

func (o *Loop) newWatcher(wt string, base Status) *watcher {
	return &watcher{o: o, wt: wt, base: base}
}

// report shows the worker in state st, as just read, reading its screen for its latest action; err
// is why the state could not be read.
func (w *watcher) report(ctx context.Context, st AgentState, err error) {
	o := w.o
	s := w.base
	s.Agent, s.Unreadable = st, err != nil
	if err == nil && st != StateGone {
		w.activity = lastActivity(o.agents.Screen(ctx, o.agentName(w.base.Ticket), st))
	}
	s.Activity = w.activity
	if o.reporter != nil && err == nil && st != StateGone {
		u, ok := o.reporter.LastToolUse(w.wt)
		rec := o.reporter.HookRecord(w.wt)
		switch {
		case st == StateWorking:
			if ok {
				s.Doing = Doing(u, o.cfg.Check)
			}
			if s.Doing == "" && rec.Subagents > 0 {
				s.Doing = DoingSubagent
			}
		case waitsOnPermission(st, u, ok):
			s.Permission = true
			w.notePermission(u)
		}
		w.noteCompactions(rec)
	}
	o.status(s)
}

// notePermission logs the permission prompt the worker reported in u, once.
func (w *watcher) notePermission(u ToolUse) {
	if !u.At.IsZero() && u.At.Equal(w.asked) {
		return
	}
	w.asked = u.At
	what := u.Tool
	if cmd := strings.Join(strings.Fields(u.Command), " "); cmd != "" {
		if r := []rune(cmd); len(r) > 80 {
			cmd = string(r[:80]) + "…"
		}
		what += " (" + cmd + ")"
	}
	id := w.base.Ticket
	w.o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
		"  PERMISSION_PROMPT: %s's worker waits on a permission prompt for %s in tab %s; allow or deny it there",
		id, what, w.base.Tab)})
}

// noteCompactions logs the compactions of the worker's context in rec that the last read hadn't.
func (w *watcher) noteCompactions(rec HookRecord) {
	if w.noted != nil {
		for _, trigger := range rec.Compactions[min(len(w.noted.Compactions), len(rec.Compactions)):] {
			w.o.info("  %s's worker's context was compacted (%s)", w.base.Ticket, trigger)
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
	agent := o.agentName(w.base.Ticket)
	go func() {
		defer close(done)
		defer func() {
			if p := recover(); p != nil { // the worker goes on; only its status stops showing until it settles
				o.emit(Event{Kind: EvWarn, Ticket: w.base.Ticket, Text: fmt.Sprintf("  WATCH_FAILED for %s: panic: %s; "+
					"its status is not shown until it takes its prompt (the stack is in %s)",
					w.base.Ticket, o.logPanic("watching "+w.base.Ticket, p), o.cfg.LogPath)})
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
