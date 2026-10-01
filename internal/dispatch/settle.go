package dispatch

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// waitSettled waits until the worker settles, and returns when it went idle for good. Never answer
// its prompts; stop if it stays blocked for 4 minutes, or in a status Herdr can't tell (unknown) for
// 5. An idle worker has settled only once idleSettled says so: Herdr takes a worker for idle while
// it starts up, and while it waits on its own background command. hooks says it reports through
// them, since from when (zero: any report counts); begun is when it was confirmed started on its
// prompt. A status Herdr fails to read says
// nothing about the worker, so the wait goes on through maxFailedReads of them in a row before the
// run stops. A worker still going Config.TicketLimit after started (dispatch) stops the run;
// without a limit, one still going after longRunning is reported once. Each status read goes to
// report (nil: none), for the dashboard.
func (o *Loop) waitSettled(
	ctx context.Context, id, agent, tab, wt string, started, begun time.Time, hooks bool, since time.Time,
	report func(ctx context.Context, st AgentState, err error),
) (idleAt time.Time, stop *stopReason) {
	var blockedSince, idleSince, unknownSince time.Time
	failed := 0
	warned := false
	for {
		if ctx.Err() != nil {
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
			if !sleep(ctx, o.pollEvery()) {
				return time.Time{}, errInterrupted
			}
			continue
		}
		failed = 0
		if st == StateGone {
			o.info("  %s settled: its worker is gone", id)
			if idleSince.IsZero() {
				return time.Now(), nil
			}
			return idleSince, nil
		}
		if st == StateIdle || st == StateDone {
			if idleSince.IsZero() {
				idleSince = time.Now()
			}
			ts, err := o.tickets.Status(ctx, id)
			if ctx.Err() != nil {
				return time.Time{}, errInterrupted // the status read was cut short, and says nothing
			}
			if err != nil {
				o.log.Raw("", err)
			}
			if done, why := o.idleSettled(ts, wt, hooks, since, time.Since(idleSince), time.Since(begun)); done {
				o.info("  %s settled: %s", id, why)
				return idleSince, nil
			}
		} else {
			idleSince = time.Time{}
		}
		if st == StateBlocked {
			if blockedSince.IsZero() {
				blockedSince = time.Now()
			}
			if time.Since(blockedSince) > orDefault(o.wait.blocked, blockedLimit) {
				return time.Time{}, halt(ExitStuck, stopBlocked, " >4min: tab %s (%s) needs attention", tab, id)
			}
		} else {
			blockedSince = time.Time{}
		}
		if st == StateUnknown {
			if unknownSince.IsZero() {
				unknownSince = time.Now()
			}
			if time.Since(unknownSince) > orDefault(o.wait.unknown, unknownLimit) {
				return time.Time{}, halt(ExitStuck, stopUnknown,
					" >5min: Herdr can't tell what the worker in tab %s (%s) is doing; it needs attention", tab, id)
			}
		} else {
			unknownSince = time.Time{}
		}
		if limit := o.cfg.TicketLimit; limit > 0 && time.Since(started) > limit {
			o.appendNotes(context.WithoutCancel(ctx), id, fmt.Sprintf(
				"Orchestra: worker in Herdr tab %s was still %s after the %s ticket limit (worktree %s).",
				tab, st, ShortDuration(limit), wt))
			return time.Time{}, halt(ExitStuck, stopTicketLimit,
				": %s still %s after %s in tab %s (worktree %s); stopping so it can be looked at",
				id, st, ShortDuration(limit), tab, wt)
		}
		if long := orDefault(o.wait.longRun, longRunning); o.cfg.TicketLimit == 0 && !warned && time.Since(started) > long {
			warned = true
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  LONG_RUNNING: %s still %s after %s in tab %s; still waiting on it, as no ticket limit is set (--ticket-limit)",
				id, st, ShortDuration(long), tab)})
		}
		if !sleep(ctx, o.pollEvery()) {
			return time.Time{}, errInterrupted
		}
	}
}

// blockedLimit is how long a worker may stay blocked before the run stops for it.
const blockedLimit = 4 * time.Minute

// unknownLimit is how long a worker's status may stay unknown (Herdr can't tell what it is doing)
// before the run stops for it.
const unknownLimit = 5 * time.Minute

// longRunning is how long a worker may go on, without a ticket limit, before the run reports it.
const longRunning = 2 * time.Hour

// maxFailedReads is how many failed status reads in a row (a minute's worth) stop the wait on a
// worker.
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
func (o *Loop) idleSettled(ticket, wt string, hooks bool, since time.Time, idleFor, running time.Duration) (
	settled bool, why string) {
	grace := orDefault(o.wait.idleGrace, idleGrace)
	if ticket == "in_progress" {
		return idleFor >= grace, fmt.Sprintf("idle for %s with the ticket still in progress", ShortDuration(grace))
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
			return idleFor >= grace, fmt.Sprintf("idle for %s after a %s hook, with no Stop hook", ShortDuration(grace), u.Event)
		}
	}
	if ticket == "open" {
		start := orDefault(o.wait.startGrace, startGrace)
		return running >= start, fmt.Sprintf("idle %s after it started, with the ticket still open", ShortDuration(start))
	}
	return true, "idle with the ticket " + ticket
}

// watcher shows a worker's status and latest action on the dashboard.
type watcher struct {
	o        *Loop
	wt       string
	base     Status
	activity string // the latest action read, kept while the screen can't be
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
	if o.reporter != nil && st == StateWorking {
		if u, ok := o.reporter.LastToolUse(w.wt); ok {
			s.Doing = Doing(u, o.cfg.Check)
		}
	}
	o.status(s)
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
