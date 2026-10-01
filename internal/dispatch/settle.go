package dispatch

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// waitSettled waits until the worker settles. Never answer its prompts; stop if it stays blocked for 4
// minutes, or in a status Herdr can't tell (unknown) for 5. A worker waiting on its own background
// command looks idle too, so an idle worker whose ticket is still in progress gets idleGrace to
// resume before it counts as settled. A status Herdr fails to read says nothing about the worker,
// so the wait goes on through maxFailedReads of them in a row before the run stops. A worker still
// going Config.TicketLimit after started (dispatch) stops the run; without a limit, one still going
// after longRunning is reported once. Each status read goes to report (nil: none), for the dashboard.
func (o *Loop) waitSettled(ctx context.Context, id, agent, tab, wt string, started time.Time, report func(status string)) *stopReason {
	var blockedSince, idleSince, unknownSince time.Time
	failed := 0
	warned := false
	for {
		if ctx.Err() != nil {
			return errInterrupted
		}
		st, err := o.agents.Status(agent)
		if report != nil {
			report(st)
		}
		if err != nil {
			if failed++; failed == 1 {
				o.log.Raw("", fmt.Errorf("cannot read the status of %s's worker; still waiting on it: %w", id, err))
			}
			if failed >= maxFailedReads {
				return halt(ExitTool, "HERDR_FAILED: the status of %s's worker (tab %s) could not be read %d times in a row: %v", id, tab, failed, err)
			}
			if !sleep(ctx, o.pollEvery()) {
				return errInterrupted
			}
			continue
		}
		failed = 0
		if st == "gone" {
			return nil
		}
		if st == "idle" || st == "done" {
			if idleSince.IsZero() {
				idleSince = time.Now()
			}
			ts, err := o.tickets.Status(id)
			if err != nil {
				o.log.Raw("", err)
			}
			if !keepWaiting(ts, time.Since(idleSince), orDefault(o.wait.idleGrace, idleGrace)) {
				return nil
			}
		} else {
			idleSince = time.Time{}
		}
		if st == "blocked" {
			if blockedSince.IsZero() {
				blockedSince = time.Now()
			}
			if time.Since(blockedSince) > orDefault(o.wait.blocked, blockedLimit) {
				return halt(ExitStuck, "BLOCKED >4min: tab %s (%s) needs attention", tab, id)
			}
		} else {
			blockedSince = time.Time{}
		}
		if st == "unknown" {
			if unknownSince.IsZero() {
				unknownSince = time.Now()
			}
			if time.Since(unknownSince) > orDefault(o.wait.unknown, unknownLimit) {
				return halt(ExitStuck, "UNKNOWN >5min: Herdr can't tell what the worker in tab %s (%s) is doing; it needs attention", tab, id)
			}
		} else {
			unknownSince = time.Time{}
		}
		if limit := o.cfg.TicketLimit; limit > 0 && time.Since(started) > limit {
			o.appendNotes(id, fmt.Sprintf("Orchestra: worker in Herdr tab %s was still %s after the %s ticket limit (worktree %s).", tab, st, ShortDuration(limit), wt))
			return halt(ExitStuck, "TICKET_LIMIT: %s still %s after %s in tab %s (worktree %s); stopping so it can be looked at", id, st, ShortDuration(limit), tab, wt)
		}
		if long := orDefault(o.wait.longRun, longRunning); o.cfg.TicketLimit == 0 && !warned && time.Since(started) > long {
			warned = true
			o.emit(Event{Kind: EvWarn, Ticket: id, Text: fmt.Sprintf(
				"  LONG_RUNNING: %s still %s after %s in tab %s; still waiting on it, as no ticket limit is set (--ticket-limit)", id, st, ShortDuration(long), tab)})
		}
		if !sleep(ctx, o.pollEvery()) {
			return errInterrupted
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

// readStatus reads the worker's status, trying up to tries times while Herdr fails to answer and
// logging each failure. "unreadable" and the last error if it never answers. agent is the
// worker's Herdr name.
func (o *Loop) readStatus(ctx context.Context, agent string, tries int) (string, error) {
	for try := 1; ; try++ {
		st, err := o.agents.Status(agent)
		if err == nil || try == tries {
			return st, err
		}
		o.log.Raw("", err)
		if !sleep(ctx, o.pollEvery()) {
			return st, err
		}
	}
}

// idleGrace is how long an idle worker whose ticket is still in progress may take to resume
// (typically it is waiting on its own background command) before the run pauses for it.
const idleGrace = 10 * time.Minute

func keepWaiting(ticketStatus string, idleFor, grace time.Duration) bool {
	return ticketStatus == "in_progress" && idleFor < grace
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

// report shows the worker in status st, as just read, reading its screen for its latest action.
func (w *watcher) report(st string) {
	o := w.o
	s := w.base
	s.Agent = st
	if st != "gone" && st != "unreadable" {
		w.activity = lastActivity(o.agents.Screen(o.agentName(w.base.Ticket), st))
	}
	s.Activity = w.activity
	if o.reporter != nil && st == "working" {
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
		tick := time.NewTicker(o.pollEvery())
		defer tick.Stop()
		for {
			st, _ := o.agents.Status(agent) // a failure shows as unreadable; the start logs its own
			if ctx.Err() != nil {
				return
			}
			w.report(st)
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
