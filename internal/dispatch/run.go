package dispatch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// SoloState is the ticket labelled SoloLabel that runs alone, or, with Next, the one next in line
// that waits for the running tickets to finish. The zero value is none.
type SoloState struct {
	Ticket string
	Next   bool
}

// Interrupted is the cause a run's context is cancelled with to say what stopped the run, as in
// "by SIGTERM". Without it the loop reports Ctrl+C.
type Interrupted string

func (i Interrupted) Error() string { return "stopped " + string(i) }

func (o *Loop) interrupted(ctx context.Context) int {
	if !o.ReportInterrupt {
		return ExitInterrupted
	}
	why := Interrupted("with Ctrl+C")
	errors.As(context.Cause(ctx), &why)
	o.emit(Event{Kind: EvStop, Text: InterruptLine(string(why), o.activeList())})
	return ExitInterrupted
}

// InterruptLine is the INTERRUPTED line for a run stopped the way why says, naming the workers
// left running.
func InterruptLine(why string, running []Status) string {
	if len(running) == 0 {
		return "INTERRUPTED: stopped " + why + "; a running worker keeps its tab and worktree"
	}
	var names []string
	for _, st := range running {
		names = append(names, fmt.Sprintf("%s (tab %s)", st.Ticket, st.Tab))
	}
	return fmt.Sprintf("INTERRUPTED: stopped %s while %s were running; their tabs and worktrees are left open", why, strings.Join(names, ", "))
}

// readyPoll is how often Run reads bd ready while workers run: tickets become ready mid-run (a
// question answered, a follow-up a worker filed, a blocker closed), and a free slot shouldn't wait
// for a running ticket to finish before picking them up.
const readyPoll = 30 * time.Second

// result is what a worker returns to Run.
type result struct {
	id   string
	stop *stopReason
}

// Run works through bd ready with up to Concurrency workers at a time and returns the exit code.
func (o *Loop) Run(ctx context.Context) int {
	c := o.cfg
	o.count = c.DoneSoFar
	o.started = time.Now()
	o.startHead = o.checkout.Head(c.Repo, c.Base)
	ticketLimit := "none"
	if c.TicketLimit > 0 {
		ticketLimit = ShortDuration(c.TicketLimit)
	}
	o.info("START orchestra %s in %s on %s (done so far: %d, limit: %d, concurrent: %d, ticket limit: %s, check timeout: %s, workspace: %s, agent: %s, worktrees: %s)",
		c.Version, c.Repo, c.Base, o.count, c.Limit, c.Concurrency, ticketLimit, ShortDuration(o.checkTimeout()), c.Workspace, c.AgentKind, c.WTRoot)
	if s := o.loadUnmerged(); s != nil {
		return o.stop(s.code, "%s", s.text)
	}

	results := make(chan result, c.Concurrency) // buffered: a worker finishing after settle never blocks
	inflight := map[string]bool{}
	o.queued = -1
	poll := time.NewTicker(orDefault(o.wait.ready, readyPoll))
	defer poll.Stop()
	var stop *stopReason // the first reason decides the exit code
	var alsoStopped []string
	envSaid := false
	for {
		// Workers failing at once, whichever ticket they have, hold the run: said once, before
		// another ticket starts.
		if s := o.environmentStop(); s != nil && !envSaid {
			envSaid = true
			if stop == nil {
				stop = s
			} else {
				alsoStopped = append(alsoStopped, s.text)
			}
			if len(inflight) > 0 {
				o.emit(Event{Kind: EvHold, Text: fmt.Sprintf(
					"HOLD: %s; no new tickets while the %d running finish", s.text, len(inflight))})
			}
		}
		// Start tickets while there are free slots, unless something has stopped the run.
		for stop == nil && ctx.Err() == nil && len(inflight) < c.Concurrency && o.count < c.Limit {
			t, queued, s := o.next(inflight)
			if s != nil {
				stop = s
				if len(inflight) > 0 {
					o.emit(Event{Kind: EvHold, Text: fmt.Sprintf(
						"HOLD: %s; no new tickets while the %d running finish", s.text, len(inflight))})
				}
				break
			}
			if t == nil {
				o.reportQueue(queued)
				break // nothing ready that isn't already running, or it waits for a solo ticket
			}
			o.count++
			inflight[t.ID] = true
			how := "dispatching"
			if HasLabel(*t, SoloLabel) {
				o.solo, how = t.ID, "dispatching solo"
			}
			o.queued, o.soloShown = queued, o.soloState()
			o.emit(Event{Kind: EvDispatch, N: o.count, Limit: c.Limit, Ticket: t.ID, Title: t.Title, Queued: queued, Solo: o.soloShown,
				Text: fmt.Sprintf("[%d/%d] %s %s: %s", o.count, c.Limit, t.ID, how, t.Title)})
			o.startFootprint(*t)
			go func(t Ticket) { results <- result{t.ID, o.work(ctx, t)} }(*t)
		}
		if len(inflight) == 0 {
			break
		}
		select {
		case r := <-results:
			delete(inflight, r.id)
			o.endFootprint(r.id)
			if r.id == o.solo {
				o.solo = ""
			}
			if r.stop == nil || r.stop == errInterrupted {
				continue
			}
			// Every reason is shown as it arrives. The first decides the exit code; the final
			// line gives it and then the others.
			first := stop == nil
			if first {
				stop = r.stop
			} else {
				alsoStopped = append(alsoStopped, r.stop.text)
			}
			switch {
			case len(inflight) > 0:
				o.emit(Event{Kind: EvHold, Ticket: r.id, Text: fmt.Sprintf(
					"HOLD: %s; no new tickets while the %d running finish", r.stop.text, len(inflight))})
			case !first:
				o.emit(Event{Kind: EvHold, Ticket: r.id, Text: "HOLD: " + r.stop.text})
			}
		case <-o.envWake: // the hold is picked up above
		case <-poll.C:
			if o.footprintOn() && len(inflight) > 1 {
				o.readEdits() // warns when two workers edit the same file
			}
			// With a free slot, the loop above reads bd ready again. With none, the queue count is
			// brought up to date; a failed read waits for the next poll, as nothing depends on it.
			if stop == nil && len(inflight) >= c.Concurrency && o.count < c.Limit {
				if t, queued, err := o.pick(inflight); err == nil {
					if t != nil {
						queued++
					}
					o.reportQueue(queued)
				}
			}
		case <-ctx.Done():
			o.settle(results, inflight)
			return o.interrupted(ctx)
		}
	}
	switch {
	case ctx.Err() != nil:
		return o.interrupted(ctx)
	case stop != nil:
		text := stop.text
		for _, t := range alsoStopped {
			text += "; also " + t
		}
		return o.stop(stop.code, "%s", text)
	case o.count >= c.Limit:
		o.emit(Event{Kind: EvDone, Text: fmt.Sprintf("LIMIT_REACHED at %d tickets", o.count)})
	default:
		o.emit(Event{Kind: EvDone, Text: fmt.Sprintf("READY_EMPTY after %d tickets", o.count)})
	}
	return ExitOK
}

// SettleWait is how long Run waits after Ctrl+C for its workers to return, so what follows (triage,
// the run report) reads a loop that has stopped changing. A worker notices the cancellation within
// seconds unless a command it runs hangs.
const SettleWait = 10 * time.Second

// settle waits for the workers in flight to return, or for SettleWait.
func (o *Loop) settle(results <-chan result, inflight map[string]bool) {
	timeout := time.NewTimer(orDefault(o.wait.settle, SettleWait))
	defer timeout.Stop()
	for len(inflight) > 0 {
		select {
		case r := <-results:
			delete(inflight, r.id)
		case <-timeout.C:
			return
		}
	}
}

// next returns the highest-priority ready ticket not already running, with how many others are
// ready, or a reason to stop. The main checkout must be clean and on Base, since finished tickets
// are fast-forwarded into it; the check waits for any merge in progress.
func (o *Loop) next(running map[string]bool) (*Ticket, int, *stopReason) {
	o.repoMu.Lock()
	s := o.checkoutUnready("")
	o.repoMu.Unlock()
	if s != nil {
		return nil, 0, s
	}
	t, queued, err := o.pick(running)
	if err != nil {
		return nil, 0, halt(ExitTool, "READY_UNREADABLE: could not read 'bd ready --json'%s", because(err))
	}
	return t, queued, nil
}

// reportQueue tells the dashboard how many ready tickets wait for a slot, and which solo ticket
// runs or is next, when that has changed.
func (o *Loop) reportQueue(n int) {
	solo := o.soloState()
	if n == o.queued && solo == o.soloShown {
		return
	}
	o.queued, o.soloShown = n, solo
	o.emit(Event{Kind: EvQueue, Queued: n, Solo: solo})
}

// soloState is the solo ticket running, or else the one next in line.
func (o *Loop) soloState() SoloState {
	switch {
	case o.solo != "":
		return SoloState{Ticket: o.solo}
	case o.soloNext != "":
		return SoloState{Ticket: o.soloNext, Next: true}
	}
	return SoloState{}
}

// pick reads bd ready and returns the highest-priority ticket that can start, with how many others
// could; with none, how many wait for a solo ticket. It says once why they wait.
func (o *Loop) pick(running map[string]bool) (*Ticket, int, error) {
	ready, err := o.tickets.Ready()
	if err != nil {
		return nil, 0, err
	}
	// A ticket set aside in this run stays out of it, even if bd still lists it as ready (a defer
	// that failed, say); otherwise it would be dispatched again at once, in a new tab each time. A
	// ticket waiting on a question is the exception: bd keeps it out of ready until the question is
	// answered, and then it comes back.
	skip := map[string]bool{}
	for _, id := range o.setAside() {
		skip[id] = !o.isAsked(id)
	}
	for id := range running {
		skip[id] = true
	}
	// With a free slot, a ticket whose footprint overlaps a running ticket's is skipped for the next
	// one that doesn't; with none free, the tickets wait for a slot anyway.
	slot := len(running) < o.cfg.Concurrency
	overlaps := func(Ticket) bool { return false }
	if slot && o.footprintOn() {
		o.readFiles()
		if len(running) > 0 {
			o.readEdits()
			overlaps = func(t Ticket) bool { return o.overlapsRunning(t, running) }
		}
	}
	t, queued, next := pickNext(ready, skip, func(t Ticket) bool { return o.held(t, running) || overlaps(t) }, len(running), o.solo)
	// A solo ticket holds something back only when a slot is free; with every slot taken (always,
	// with one worker) the tickets wait for a slot as they would anyway.
	if !slot {
		next = ""
	}
	o.soloNext = next
	why := ""
	switch {
	case slot && t == nil && queued > 0 && o.solo != "":
		why = fmt.Sprintf("waiting for solo ticket %s to finish", o.solo)
	case next != "":
		why = fmt.Sprintf("solo ticket %s is next: no new tickets start until the running ones finish, then it runs alone", next)
	}
	if why != "" && why != o.soloSaid {
		o.info("  %s", why)
	}
	o.soloSaid = why
	return t, queued, nil
}

// checkoutUnready returns a reason to stop when the main checkout has uncommitted changes or is
// not on Base, or nil. A fast-forward there moves whichever branch is checked out, so it must be
// Base. held, when set, says what is left for review. The caller holds repoMu.
func (o *Loop) checkoutUnready(held string) *stopReason {
	c := o.cfg
	if o.checkout.DirtyTree(c.Repo) != "" {
		return halt(ExitDirty, "DIRTY_TREE: uncommitted changes in %s; stopping%s. Inspect with: git status", c.Repo, held)
	}
	if o.checkout.CurrentBranch(c.Repo) != c.Base {
		return halt(ExitDirty, "DIRTY_TREE: %s is no longer on %s; stopping%s. Check it out again to continue.", c.Repo, c.Base, held)
	}
	return nil
}

// pickNext returns the first ticket (ready is in priority order) that isn't skipped or held, and
// how many other ready tickets aren't either. A ticket labelled SoloLabel runs alone: while solo
// (the one running, or "") runs nothing starts, and one first in line starts only once none of
// the running tickets are left, holding back the tickets behind it until then so it isn't
// starved; next names it. With no ticket to start, queued is how many wait.
func pickNext(ready []Ticket, skip map[string]bool, held func(Ticket) bool, running int, solo string) (t *Ticket, queued int, next string) {
	var free []Ticket
	for _, t := range ready {
		if !skip[t.ID] && !held(t) {
			free = append(free, t)
		}
	}
	switch {
	case len(free) == 0:
		return nil, 0, ""
	case solo != "":
		return nil, len(free), ""
	case HasLabel(free[0], SoloLabel) && running > 0:
		return nil, len(free), free[0].ID
	}
	return &free[0], len(free) - 1, ""
}
