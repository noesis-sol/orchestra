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
		if st.Fixing {
			names = append(names, fmt.Sprintf("%s (tab %s, fixing its check: once its worker has committed the fix, "+
				"run the check and merge it)", st.Ticket, st.Tab))
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
	o.begin(ctx)
	// Ctrl+C as the last run's state loads ends the run at once: what Run read since may be cut short,
	// and leaving behind what it carried would save .orchestra/run/state.json without the workers it
	// could not check. Left as it was, the file carries them all over to the next run.
	s := o.loadUnmerged(ctx)
	if ctx.Err() != nil {
		return o.interrupted(ctx)
	}
	if s != nil {
		return o.stop(s, s.Error())
	}
	if o.loadCarried(ctx); ctx.Err() != nil {
		return o.interrupted(ctx)
	}
	defer o.startPredicting()()

	r := newRunState(ctx, o)
	poll := time.NewTicker(readyPoll)
	defer poll.Stop()
	r.follow() // the workers carried over from the last run that closed their tickets, or work on them again
	for {
		r.winding()
		r.heedEnvironment()
		r.sayNoMore()
		r.recheckTickets() // before new tickets: each is done but for its merge, and others may wait on it
		r.startTickets()
		if len(r.inflight) == 0 {
			if r.idle() {
				continue
			}
			return r.end()
		}
		select {
		case res := <-r.results:
			r.returned(res)
		case v := <-o.verdicts:
			o.triaged(r.keep, v) // a hold is picked up above
		case req := <-o.drainReqs:
			r.hear(req)
		case <-poll.C:
			r.polled()
		case <-ctx.Done():
			return r.end()
		}
	}
}

// begin notes when the run starts and Base's commit then, and says how it runs.
func (o *Loop) begin(ctx context.Context) {
	c := o.cfg
	o.started = time.Now()
	o.startHead = o.checkout.Head(ctx, c.Repo, c.Base)
	ticketLimit := "none"
	if c.TicketLimit > 0 {
		ticketLimit = command.ShortDuration(c.TicketLimit)
	}
	o.info("START orchestra %s in %s on %s%s (done so far: %d, limit: %d, concurrent: %d, ticket limit: %s, "+
		"check timeout: %s, workspace: %s, agent: %s, worktrees: %s, MCP servers: %s)",
		c.Version, c.Repo, c.Base, ScopeLabel(c), c.DoneSoFar, c.Limit, c.Concurrency, ticketLimit,
		command.ShortDuration(o.checkTimeout()), c.Workspace, c.AgentKind, c.WTRoot, c.mcpLabel())
	if c.Feature != "" {
		o.info("  feature: epic %s, planned from: %s", c.Ticket, FeatureLine(c.Feature))
	}
	o.sayMCP()
}

// runState is what Run owns: only its goroutine reads or changes it. Its methods are Run's steps.
type runState struct {
	o    *Loop
	ctx  context.Context // the run's: Ctrl+C cancels it
	keep context.Context // for reopening tickets as the run holds

	results  chan result     // buffered: a worker finishing after settle never blocks
	inflight map[string]bool // the tickets whose workers haven't returned
	count    int             // the tickets dispatched, with those done in earlier runs (Config.DoneSoFar)
	stops    stops           // the reasons the run stops for, first to last
	envSaid  bool            // the hold for the environment is taken up
	drained  bool            // the maintainer asked to stop after the running tickets, as last said

	// The queue size last reported, -1 before the first, and the solo ticket with it (see reportQueue).
	queued    int
	soloShown SoloState
}

func newRunState(ctx context.Context, o *Loop) *runState {
	return &runState{
		o: o, ctx: ctx, keep: context.WithoutCancel(ctx),
		results: make(chan result, o.cfg.Concurrency), inflight: map[string]bool{},
		count: o.cfg.DoneSoFar, queued: -1,
	}
}

// stops are the reasons the run stops for, as they come: the first decides the exit code, and the
// line the run ends with gives it, then the others.
type stops struct {
	first  *stopReason
	others []string
}

// add records s and reports whether it is the first.
func (l *stops) add(s *stopReason) (first bool) {
	if l.first == nil {
		l.first = s
		return true
	}
	l.others = append(l.others, s.Error())
	return false
}

// line is the line the run ends with: the first reason, then "; also" and each of the others.
func (l *stops) line() string {
	return strings.Join(append([]string{l.first.Error()}, l.others...), "; also ")
}

// holdLine is the HOLD line for s, saying that no new tickets start while the running ones finish,
// if any do.
func holdLine(s *stopReason, running int) string {
	if running == 0 {
		return "HOLD: " + s.Error()
	}
	return fmt.Sprintf("HOLD: %s; no new tickets while the %d running finish", s, running)
}

// sayHold says that the run holds for s while tickets run. ticket is the one s came from, if any.
func (r *runState) sayHold(s *stopReason, ticket string) {
	if len(r.inflight) > 0 {
		r.o.emit(Event{Kind: EvHold, Ticket: ticket, Text: holdLine(s, len(r.inflight))})
	}
}

// taking reports whether the run takes new tickets: nothing has stopped it, the maintainer hasn't
// asked it to wind down, and Config.Limit isn't reached.
func (r *runState) taking() bool {
	return r.stops.first == nil && !r.drained && r.count < r.o.cfg.Limit
}

// hear logs a request to wind down or to take tickets again, when it changes anything, and says
// whether the run winds down.
func (r *runState) hear(req drainRequest) bool {
	if req.on != r.drained {
		r.drained = req.on
		r.o.emit(drainEvent(req.on, req.how, r.inflight))
	}
	return r.drained
}

// winding hears a request waiting, if any; asked just before a ticket would start, it keeps
// that ticket from starting.
func (r *runState) winding() bool {
	select {
	case req := <-r.o.drainReqs:
		return r.hear(req)
	default:
		return r.drained
	}
}

// sayNoMore tells the merges why the run takes on no more work, if it doesn't: it holds for a stop, or
// the maintainer asked it to wind down (see Loop.noMore).
func (r *runState) sayNoMore() {
	why := ""
	switch {
	case r.stops.first != nil:
		why = "the run holds (" + r.stops.first.Error() + ")"
	case r.drained:
		why = "the run is winding down"
	}
	r.o.mu.Lock()
	r.o.noMore = why
	r.o.mu.Unlock()
}

// heedEnvironment takes up the hold for the environment, once workers fail at once whichever
// ticket they have: said once, before another ticket starts.
func (r *runState) heedEnvironment() {
	s := r.o.envStop
	if s == nil || r.envSaid {
		return
	}
	r.envSaid = true
	r.stops.add(s)
	r.sayHold(s, "")
}

// startTickets starts tickets while there are free slots, unless something has stopped the run or
// the maintainer asked it to wind down.
func (r *runState) startTickets() {
	for r.taking() && r.ctx.Err() == nil && len(r.inflight) < r.o.cfg.Concurrency {
		t, queued, s := r.o.next(r.ctx, r.inflight)
		if s != nil {
			r.stops.add(s)
			r.sayHold(s, "")
			return
		}
		if t == nil {
			r.reportQueue(queued)
			return // nothing ready that isn't already running, or it waits for a solo ticket
		}
		if r.winding() {
			return
		}
		r.start(*t, queued)
	}
}

// start hands ticket t to a worker, with queued tickets ready behind it.
func (r *runState) start(t Ticket, queued int) {
	o, c := r.o, r.o.cfg
	r.count++
	o.setParent(t.ID, t.Parent)
	how := "dispatching"
	if HasLabel(t, SoloLabel) {
		o.solo, how = t.ID, "dispatching solo"
	}
	if w, ok := o.asked(t.ID); ok && w.question != "" {
		o.emit(Event{Kind: EvAnswered, Ticket: t.ID, Title: t.Title, Detail: w.question + ": " + w.title, Text: fmt.Sprintf(
			"  ANSWERED: %s (%s) is answered, so %s comes back", w.question, w.title, t.ID)})
	}
	r.queued, r.soloShown = queued, o.soloState()
	o.emit(Event{Kind: EvDispatch, N: r.count, Limit: c.Limit, Ticket: t.ID, Title: t.Title,
		Queued: queued, Solo: r.soloShown, Text: fmt.Sprintf("[%d/%d] %s %s: %s", r.count, c.Limit, t.ID, how, t.Title)})
	o.startFootprint(t)
	r.launch(t.ID, func(ctx context.Context, how *settling) *stopReason { return o.work(ctx, t, how) })
}

// launch runs ticket id's worker, work, on a goroutine of its own, which hands its result to Run.
// The ticket runs from now until Run takes the result.
func (r *runState) launch(id string, work func(ctx context.Context, how *settling) *stopReason) {
	r.inflight[id] = true
	ctx, results := r.ctx, r.results
	go func() {
		res := result{id: id}
		res.stop = work(ctx, &res.how)
		results <- res
	}()
}

// follow adopts the asked tickets whose workers claimed them again or closed them in their tabs
// (see followAsked), and reports whether it adopted any. Their workers are at work already, so
// they count as running at once, even beyond Concurrency. Once something has stopped the run,
// they are left to leaveAsked.
func (r *runState) follow() bool {
	o := r.o
	if r.stops.first != nil || r.ctx.Err() != nil {
		return false
	}
	adopt, s := o.followAsked(r.ctx, r.inflight, r.taking())
	for _, a := range adopt {
		r.launch(a.t.ID, func(ctx context.Context, how *settling) *stopReason {
			return o.adoptAsked(ctx, a, how)
		})
	}
	if s != nil {
		r.stops.add(s)
	}
	return len(adopt) > 0
}

// idle is Run's step once nothing runs, and reports whether the run goes on: asked tickets were
// adopted, or the run held for the environment and a probe found that commands run again.
func (r *runState) idle() bool {
	// Before the run ends, the asked tickets are read once more.
	if r.follow() {
		return true
	}
	// Held for the environment, the run may probe the machine and take tickets again.
	s := r.stops.first
	if s == nil || len(r.stops.others) > 0 {
		return false
	}
	if r.stops.first = r.o.probeEnvironment(r.ctx, s, r.winding, r.hear); r.stops.first != nil {
		return false
	}
	r.envSaid = false
	return true
}

// returned takes a worker's result: its ticket no longer runs, and the reason it stopped the run
// for, if any, holds the run.
func (r *runState) returned(res result) {
	o := r.o
	delete(r.inflight, res.id)
	o.stoppedBy(res.id, res.stop)
	o.endFootprint(res.id)
	o.settled(r.keep, res.id, res.how)
	if res.id == o.solo {
		o.solo = ""
	}
	if res.stop == nil {
		o.parentDone(r.ctx, res.id, r.inflight)
	}
	if res.stop == nil || res.stop == errInterrupted {
		return
	}
	// Every reason is shown as it arrives. The first decides the exit code; the final line gives it
	// and then the others.
	if first := r.stops.add(res.stop.over(res.id)); len(r.inflight) > 0 || !first {
		o.emit(Event{Kind: EvHold, Ticket: res.id, Blocked: res.stop.blocked, Text: holdLine(res.stop, len(r.inflight))})
	}
}

// polled is Run's step on each poll: it follows the asked tickets, warns of edits the workers
// share and, with every slot taken, brings the queue count up to date.
func (r *runState) polled() {
	o := r.o
	r.follow()
	if o.footprintOn() && len(r.inflight) > 1 {
		o.readEdits() // warns when two workers edit the same file
	}
	// With a free slot, Run reads bd ready again as it starts tickets. With none, the queue count is
	// brought up to date; a failed read waits for the next poll, as nothing depends on it.
	if r.taking() && len(r.inflight) >= o.cfg.Concurrency {
		if t, queued, err := o.pick(r.ctx, r.inflight); err == nil {
			if t != nil {
				queued++
			}
			r.reportQueue(queued)
		}
	}
}

// reportQueue tells the dashboard how many ready tickets wait for a slot, and which solo ticket
// runs or is next, when that has changed.
func (r *runState) reportQueue(n int) {
	solo := r.o.soloState()
	if n == r.queued && solo == r.soloShown {
		return
	}
	r.queued, r.soloShown = n, solo
	r.o.emit(Event{Kind: EvQueue, Queued: n, Solo: solo})
}

// end ends the run, once nothing runs or Ctrl+C came, and returns its exit code. It leaves behind
// the workers that stopped the run, if any, and those waiting on a question, and says how the run
// ended.
func (r *runState) end() int {
	o, c := r.o, r.o.cfg
	o.settle(r.keep, r.results, r.inflight) // after Ctrl+C: otherwise none is in flight
	o.leaveBehind(r.ctx)
	var end string
	switch {
	case r.ctx.Err() != nil:
		return o.interrupted(r.ctx)
	case r.stops.first != nil:
		return o.stop(r.stops.first, r.stops.line())
	case r.drained:
		end = fmt.Sprintf("DRAINED after %d tickets", r.count)
	case r.count >= c.Limit:
		end = fmt.Sprintf("LIMIT_REACHED at %d tickets", r.count)
	default:
		end = fmt.Sprintf("READY_EMPTY after %d tickets", r.count)
	}
	o.emit(Event{Kind: EvDone, N: r.count, Limit: c.Limit, Text: end + o.endScope(r.ctx)})
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
// Base. held, when set, says what is left for review: a finished ticket, which the reason then
// blocks from merging. The caller holds repoMu.
func (o *Loop) checkoutUnready(ctx context.Context, held string) *stopReason {
	c := o.cfg
	stop := func(s *stopReason, why string) *stopReason {
		if held == "" {
			return s
		}
		return s.blocks(why)
	}
	dirty, branch, err := o.readCheckout(ctx)
	if err != nil {
		return stop(halt(ExitTool, stopGitFailed,
			": could not read the state of %s%s; stopping%s", c.Repo, because(err), held).causedBy(err), "git failed")
	}
	if dirty != "" {
		return stop(halt(ExitDirty, stopDirtyTree,
			": uncommitted changes in %s; stopping%s. Inspect with: git status", c.Repo, held),
			"main checkout has uncommitted changes")
	}
	if branch != c.Base {
		return stop(halt(ExitDirty, stopDirtyTree,
			": %s is no longer on %s; stopping%s. Check it out again to continue.", c.Repo, c.Base, held),
			"main checkout is not on "+c.Base)
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
