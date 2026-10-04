package dispatch

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// SoloState is the ticket labelled SoloLabel that runs alone, or, with Next, the one next in line
// that waits for the running tickets to finish. The zero value is none.
type SoloState struct {
	Ticket string
	Next   bool
}

// InterruptedError is the cause a run's context is cancelled with to say what stopped the run, as
// in "by SIGTERM". Without it the loop reports Ctrl+C.
type InterruptedError string

func (i InterruptedError) Error() string { return "stopped " + string(i) }

func (o *Loop) interrupted(ctx context.Context) int {
	if !o.ReportInterrupt {
		return ExitInterrupted
	}
	why := InterruptedError("with Ctrl+C")
	errors.As(context.Cause(ctx), &why)
	o.emit(Event{Kind: EvStop, Detail: Interrupted, Text: InterruptLine(string(why), o.activeList())})
	return ExitInterrupted
}

// InterruptLine is the INTERRUPTED line for a run stopped the way why says, naming the workers
// left running.
func InterruptLine(why string, running []Status) string {
	if len(running) == 0 {
		return "INTERRUPTED: stopped " + why + "; a running worker keeps its tab and worktree"
	}
	return fmt.Sprintf("INTERRUPTED: stopped %s while %s were running; their tabs and worktrees are left open",
		why, workerNames(running))
}

// workerNames names the running workers with their tabs, for a line about the workers a stop
// leaves running.
func workerNames(running []Status) string {
	var names []string
	for _, st := range running {
		if st.Resolving {
			// Its worker may still finish the rebase; either way it is resumed by hand.
			names = append(names, fmt.Sprintf("%s (tab %s, resolving conflicts: its rebase is left in progress: "+
				"finish it (resolve, git rebase --continue), run the check and merge it)", st.Ticket, st.Tab))
			continue
		}
		names = append(names, fmt.Sprintf("%s (tab %s)", st.Ticket, st.Tab))
	}
	return strings.Join(names, ", ")
}

// QuitLine is the INTERRUPTED line for orchestra quitting at once, the way why says, rather than
// wait for a stopped run to wind down or its organs. It names the workers left running and what
// was abandoned under way (Finishing): the commands it had started run on and may still finish,
// so git may be left part way through, its lock files held.
func QuitLine(why string, running []Status, abandoned []Finishing) string {
	line := "INTERRUPTED: quit at once " + why
	switch len(running) {
	case 0:
	case 1:
		line += ", leaving " + workerNames(running) + " running with its tab and worktree open"
	default:
		line += ", leaving " + workerNames(running) + " running with their tabs and worktrees open"
	}
	if len(abandoned) > 0 {
		line += fmt.Sprintf("; abandoned %s: git may still finish %s, so check git status before starting another run",
			finishingList(abandoned), pronoun(abandoned))
	}
	return line
}

// UnderWayLine says what a stopped run is finishing (Finishing), which a further stop signal
// would abandon: again says how to send one, as in "press Ctrl+C again".
func UnderWayLine(under []Finishing, again string) string {
	verb, risk := "is", "its worktree may be left half made"
	if len(under) > 1 {
		verb, risk = "are", "their worktrees may be left half made"
	}
	if slices.ContainsFunc(under, func(f Finishing) bool { return f.What == finishMerge }) {
		risk = "the repository may be left half merged"
	}
	return fmt.Sprintf("  %s %s under way; %s to abandon %s (%s)", finishingList(under), verb, again, pronoun(under), risk)
}

// finishingList names what is under way, as in "A's merge and B's worktree setup".
func finishingList(under []Finishing) string {
	l := make([]string, len(under))
	for i, f := range under {
		l[i] = f.Ticket + "'s " + f.What
	}
	if len(l) == 1 {
		return l[0]
	}
	return strings.Join(l[:len(l)-1], ", ") + " and " + l[len(l)-1]
}

// pronoun is "it" for one thing under way and "them" for several.
func pronoun(under []Finishing) string {
	if len(under) == 1 {
		return "it"
	}
	return "them"
}

// readyPoll is how often Run reads bd ready while workers run: tickets become ready mid-run (a
// question answered, a follow-up a worker filed, a blocker closed), and a free slot shouldn't wait
// for a running ticket to finish before picking them up.
const readyPoll = 30 * time.Second

// result is what a worker returns to Run.
type result struct {
	id   string
	stop *stopReason
	how  settling // for the hold for the environment
}

// Run works through bd ready (in a scoped run, one ticket and its descendants) with up to
// Concurrency workers at a time and returns the exit code.
func (o *Loop) Run(ctx context.Context) int {
	defer close(o.runDone) // triage counts its verdicts itself from here
	c := o.cfg
	o.count = c.DoneSoFar
	o.started = time.Now()
	o.startHead = o.checkout.Head(ctx, c.Repo, c.Base)
	ticketLimit := "none"
	if c.TicketLimit > 0 {
		ticketLimit = command.ShortDuration(c.TicketLimit)
	}
	o.info("START orchestra %s in %s on %s%s (done so far: %d, limit: %d, concurrent: %d, ticket limit: %s, "+
		"check timeout: %s, workspace: %s, agent: %s, worktrees: %s, MCP servers: %s)",
		c.Version, c.Repo, c.Base, ScopeLabel(c), o.count, c.Limit, c.Concurrency, ticketLimit,
		command.ShortDuration(o.checkTimeout()), c.Workspace, c.AgentKind, c.WTRoot, c.mcpLabel())
	if c.Feature != "" {
		o.info("  feature: epic %s, planned from: %s", c.Ticket, FeatureLine(c.Feature))
	}
	o.sayMCP()
	if s := o.loadUnmerged(ctx); s != nil {
		return o.stop(s, s.Error())
	}
	o.loadCarried(ctx)
	defer o.startPredicting()()

	results := make(chan result, c.Concurrency) // buffered: a worker finishing after settle never blocks
	inflight := map[string]bool{}
	o.queued = -1
	poll := time.NewTicker(readyPoll)
	defer poll.Stop()
	var stop *stopReason // the first reason decides the exit code
	var alsoStopped []string
	envSaid := false
	drained := false // the maintainer asked to stop after the running tickets, as last said
	// hear logs a request to wind down or to take tickets again, when it changes anything, and says
	// whether the run winds down.
	hear := func(r drainRequest) bool {
		if r.on != drained {
			drained = r.on
			o.emit(drainEvent(r.on, r.how, inflight))
		}
		return drained
	}
	// winding hears a request waiting, if any; asked just before a ticket would start, it keeps
	// that ticket from starting.
	winding := func() bool {
		select {
		case r := <-o.drainReqs:
			return hear(r)
		default:
			return drained
		}
	}
	keep := context.WithoutCancel(ctx) // for reopening tickets as the run holds
	// follow adopts the asked tickets whose workers claimed them again or closed them in their tabs
	// (see followAsked), and reports whether it adopted any. Their workers are at work already, so
	// they count as running at once, even beyond Concurrency. Once something has stopped the run,
	// they are left to leaveAsked.
	follow := func() bool {
		if stop != nil || ctx.Err() != nil {
			return false
		}
		adopt, s := o.followAsked(ctx, inflight, !drained && o.count < c.Limit)
		for _, a := range adopt {
			inflight[a.t.ID] = true
			go func(a adoption) {
				r := result{id: a.t.ID}
				r.stop = o.adoptAsked(ctx, a, &r.how)
				results <- r
			}(a)
		}
		if s != nil {
			stop = s
		}
		return len(adopt) > 0
	}
	follow() // the workers carried over from the last run that closed their tickets, or work on them again
	for {
		winding()
		// Workers failing at once, whichever ticket they have, hold the run: said once, before
		// another ticket starts.
		if s := o.envStop; s != nil && !envSaid {
			envSaid = true
			if stop == nil {
				stop = s
			} else {
				alsoStopped = append(alsoStopped, s.Error())
			}
			if len(inflight) > 0 {
				o.emit(Event{Kind: EvHold, Text: fmt.Sprintf(
					"HOLD: %s; no new tickets while the %d running finish", s, len(inflight))})
			}
		}
		// Start tickets while there are free slots, unless something has stopped the run or the
		// maintainer asked it to wind down.
		for stop == nil && !drained && ctx.Err() == nil && len(inflight) < c.Concurrency && o.count < c.Limit {
			t, queued, s := o.next(ctx, inflight)
			if s != nil {
				stop = s
				if len(inflight) > 0 {
					o.emit(Event{Kind: EvHold, Text: fmt.Sprintf(
						"HOLD: %s; no new tickets while the %d running finish", s, len(inflight))})
				}
				break
			}
			if t == nil {
				o.reportQueue(queued)
				break // nothing ready that isn't already running, or it waits for a solo ticket
			}
			if winding() {
				break
			}
			o.count++
			inflight[t.ID] = true
			o.setParent(t.ID, t.Parent)
			how := "dispatching"
			if HasLabel(*t, SoloLabel) {
				o.solo, how = t.ID, "dispatching solo"
			}
			if w, ok := o.asked(t.ID); ok && w.question != "" {
				o.emit(Event{Kind: EvAnswered, Ticket: t.ID, Title: t.Title, Detail: w.question + ": " + w.title, Text: fmt.Sprintf(
					"  ANSWERED: %s (%s) is answered, so %s comes back", w.question, w.title, t.ID)})
			}
			o.queued, o.soloShown = queued, o.soloState()
			o.emit(Event{Kind: EvDispatch, N: o.count, Limit: c.Limit, Ticket: t.ID, Title: t.Title,
				Queued: queued, Solo: o.soloShown, Text: fmt.Sprintf("[%d/%d] %s %s: %s", o.count, c.Limit, t.ID, how, t.Title)})
			o.startFootprint(*t)
			go func(t Ticket) {
				r := result{id: t.ID}
				r.stop = o.work(ctx, t, &r.how)
				results <- r
			}(*t)
		}
		if len(inflight) == 0 {
			// Before the run ends, the asked tickets are read once more.
			if follow() {
				continue
			}
			// Held for the environment, the run may probe the machine and take tickets again.
			if stop == nil || len(alsoStopped) > 0 {
				break
			}
			if stop = o.probeEnvironment(ctx, stop, winding, hear); stop != nil {
				break
			}
			envSaid = false
			continue
		}
		select {
		case r := <-results:
			delete(inflight, r.id)
			o.stoppedBy(r.id, r.stop)
			o.endFootprint(r.id)
			o.settled(keep, r.id, r.how)
			if r.id == o.solo {
				o.solo = ""
			}
			if r.stop == nil {
				o.parentDone(ctx, r.id, inflight)
			}
			if r.stop == nil || r.stop == errInterrupted {
				continue
			}
			// Every reason is shown as it arrives. The first decides the exit code; the final
			// line gives it and then the others.
			first := stop == nil
			if first {
				stop = r.stop.over(r.id)
			} else {
				alsoStopped = append(alsoStopped, r.stop.Error())
			}
			switch {
			case len(inflight) > 0:
				o.emit(Event{Kind: EvHold, Ticket: r.id, Text: fmt.Sprintf(
					"HOLD: %s; no new tickets while the %d running finish", r.stop, len(inflight))})
			case !first:
				o.emit(Event{Kind: EvHold, Ticket: r.id, Text: "HOLD: " + r.stop.Error()})
			}
		case v := <-o.verdicts:
			o.triaged(keep, v) // a hold is picked up above
		case r := <-o.drainReqs:
			hear(r)
		case <-poll.C:
			follow()
			if o.footprintOn() && len(inflight) > 1 {
				o.readEdits() // warns when two workers edit the same file
			}
			// With a free slot, the loop above reads bd ready again. With none, the queue count is
			// brought up to date; a failed read waits for the next poll, as nothing depends on it.
			if stop == nil && !drained && len(inflight) >= c.Concurrency && o.count < c.Limit {
				if t, queued, err := o.pick(ctx, inflight); err == nil {
					if t != nil {
						queued++
					}
					o.reportQueue(queued)
				}
			}
		case <-ctx.Done():
			o.settle(keep, results, inflight)
			o.leaveBehind(ctx)
			return o.interrupted(ctx)
		}
	}
	o.leaveBehind(ctx) // the workers that stopped the run, if any, and those waiting on a question
	// How the loop ended by itself, if it did.
	var end string
	switch {
	case ctx.Err() != nil:
		return o.interrupted(ctx)
	case stop != nil:
		text := stop.Error()
		for _, t := range alsoStopped {
			text += "; also " + t
		}
		return o.stop(stop, text)
	case drained:
		end = fmt.Sprintf("DRAINED after %d tickets", o.count)
	case o.count >= c.Limit:
		end = fmt.Sprintf("LIMIT_REACHED at %d tickets", o.count)
	default:
		end = fmt.Sprintf("READY_EMPTY after %d tickets", o.count)
	}
	o.emit(Event{Kind: EvDone, N: o.count, Limit: c.Limit, Text: end + o.endScope(ctx)})
	return ExitOK
}

// settle waits after Ctrl+C for the workers in flight to return, so what follows (triage, the run
// report) reads a loop that has stopped changing and no worker is left behind. Each command a
// worker runs stops with the run or at its time limit, and only what must finish once begun (a
// merge under way, the notes after it) goes on, so they return within seconds, or within a
// command's time limit when one hangs. Those not back within settleSay are named, so the terminal
// doesn't sit silent meanwhile. A worker that settled still counts toward the hold.
func (o *Loop) settle(ctx context.Context, results <-chan result, inflight map[string]bool) {
	slow := time.NewTimer(settleSay)
	defer slow.Stop()
	for len(inflight) > 0 {
		select {
		case r := <-results:
			delete(inflight, r.id)
			o.stoppedBy(r.id, r.stop)
			o.settled(ctx, r.id, r.how)
		case <-slow.C:
			o.sayWaiting(inflight)
		}
	}
}

// settleSay is how long settle waits after Ctrl+C before naming the workers it waits for: most
// return within milliseconds.
const settleSay = time.Second

// sayWaiting names each worker settle waits for, with what it is finishing: its merge, say, or a
// command that has yet to stop or reach its time limit.
func (o *Loop) sayWaiting(inflight map[string]bool) {
	ids := slices.Sorted(maps.Keys(inflight))
	o.mu.Lock()
	what := make([]string, len(ids))
	for i, id := range ids {
		what[i] = cmp.Or(o.finishing[id], "last command")
	}
	o.mu.Unlock()
	for i, id := range ids {
		o.emit(Event{Kind: EvInfo, Ticket: id, Text: fmt.Sprintf("  waiting for %s's %s to finish…", id, what[i])})
	}
}

// next returns the highest-priority ready ticket not already running, with how many others are
// ready, or a reason to stop. The main checkout must be clean and on Base, since finished tickets
// are fast-forwarded into it; the check waits for any merge in progress.
func (o *Loop) next(ctx context.Context, running map[string]bool) (*Ticket, int, *stopReason) {
	o.repoMu.Lock()
	s := o.checkoutUnready(ctx, "")
	o.repoMu.Unlock()
	if ctx.Err() != nil {
		return nil, 0, nil // Ctrl+C: nothing starts, and a read it cut short says nothing
	}
	if s != nil {
		return nil, 0, s
	}
	t, queued, err := o.pick(ctx, running)
	if ctx.Err() != nil {
		return nil, 0, nil
	}
	if err != nil {
		what := "'bd ready --json'"
		if errors.As(err, new(listUnreadableError)) {
			what = "'bd list --json'"
		}
		return nil, 0, halt(ExitTool, stopReadyUnreadable, ": could not read %s%s", what, because(err)).causedBy(err)
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

// pick reads bd ready (in a scoped run, the scope's part of it) and returns the highest-priority
// ticket that can start, with how many others could; with none, how many wait for a solo ticket.
// It says once why they wait.
func (o *Loop) pick(ctx context.Context, running map[string]bool) (*Ticket, int, error) {
	ready, err := o.tickets.Ready(ctx, o.cfg.Ticket)
	if err != nil {
		return nil, 0, err
	}
	parents, err := o.openParents(ctx, running)
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
	// A ticket whose ID isn't a plain name is set aside before its ID is made a path or a branch.
	o.setAsideBadIDs(context.WithoutCancel(ctx), ready, skip)
	// With a free slot, a ticket whose footprint overlaps a running ticket's is skipped for the next
	// one that doesn't; with none free, the tickets wait for a slot anyway.
	slot := len(running) < o.cfg.Concurrency
	overlaps := func(Ticket) bool { return false }
	if slot && o.footprintOn() {
		o.readFiles(ctx)
		if len(running) > 0 {
			o.readEdits()
			overlaps = func(t Ticket) bool { return o.overlapsRunning(t, running) }
		}
	}
	if o.footprintOn() {
		o.queuePredictions(ready, skip)
	}
	held := func(t Ticket) bool { return o.held(ctx, t, running, parents) || overlaps(t) }
	t, queued, next := pickNext(ready, skip, held, len(running), o.solo)
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
		why = fmt.Sprintf(
			"solo ticket %s is next: no new tickets start until the running ones finish, then it runs alone", next)
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
func (o *Loop) checkoutUnready(ctx context.Context, held string) *stopReason {
	c := o.cfg
	dirty, branch, err := o.readCheckout(ctx)
	if err != nil {
		return halt(ExitTool, stopGitFailed,
			": could not read the state of %s%s; stopping%s", c.Repo, because(err), held).causedBy(err)
	}
	if dirty != "" {
		return halt(ExitDirty, stopDirtyTree,
			": uncommitted changes in %s; stopping%s. Inspect with: git status", c.Repo, held)
	}
	if branch != c.Base {
		return halt(ExitDirty, stopDirtyTree,
			": %s is no longer on %s; stopping%s. Check it out again to continue.", c.Repo, c.Base, held)
	}
	return nil
}

// gitTries is how many times readCheckout asks git before giving up: a git call can fail for a
// moment on a busy machine, and that says nothing about the checkout.
const gitTries = 3

// readCheckout returns the main checkout's uncommitted changes and its branch, or git's error.
func (o *Loop) readCheckout(ctx context.Context) (dirty, branch string, err error) {
	c := o.cfg
	for try := 1; ; try++ {
		if dirty, err = o.checkout.DirtyTree(ctx, c.Repo); err == nil {
			if branch, err = o.checkout.CurrentBranch(ctx, c.Repo); err == nil {
				return dirty, branch, nil
			}
		}
		if try == gitTries || ctx.Err() != nil || !sleep(ctx, o.pollEvery()) {
			return "", "", err
		}
		o.log.Raw("", err) // tried again; only the error returned is the caller's to report
	}
}

// pickNext returns the first ticket (ready is in priority order) that isn't skipped or held, and
// how many other ready tickets aren't either. A ticket labelled SoloLabel runs alone: while solo
// (the one running, or "") runs nothing starts, and one first in line starts only once none of
// the running tickets are left, holding back the tickets behind it until then so it isn't
// starved; next names it. With no ticket to start, queued is how many wait.
func pickNext(
	ready []Ticket, skip map[string]bool, held func(Ticket) bool, running int, solo string,
) (t *Ticket, queued int, next string) {
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
