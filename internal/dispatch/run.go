package dispatch

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/noesis-sol/orchestra/internal/command"
)

// SoloState is the ticket labelled SoloLabel that runs alone, or, with Next, the one next in line
// that waits for the running tickets to finish. The zero value is none.
type SoloState struct {
	Ticket string
	Next   bool
}

// HeldBack is how many ready tickets wait for something besides a free slot, by what holds them
// back; none of them counts among those queued for a slot. The zero value is none.
type HeldBack struct {
	Overlap int // their footprint overlaps a running ticket's
	Parent  int // their subtickets are not all closed and merged
	Merge   int // a ticket blocking them, or the one they were filed from, has not merged
	Solo    int // a solo ticket runs, or is next and waits for the running tickets to finish
	Setup   int // the setup failed in their worktrees; they wait for a ticket to merge, or for a probe
}

// Total is how many tickets h holds back.
func (h HeldBack) Total() int { return h.Overlap + h.Parent + h.Merge + h.Solo + h.Setup }

// add counts one ticket held back by kind.
func (h *HeldBack) add(kind holdKind) {
	switch kind {
	case notHeld:
	case heldOverlap:
		h.Overlap++
	case heldParent:
		h.Parent++
	case heldSetup:
		h.Setup++
	default:
		h.Merge++
	}
}

// queue is how many ready tickets a pick found besides the one to start: waiting for a slot
// (queued), and held back.
type queue struct {
	queued int
	held   HeldBack
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

// machinePoll is how often a run held for Config.MachineWorkers reads the other runs (see machine.hold), so
// that a slot another run's worker frees is filled within seconds rather than at the next readyPoll.
const machinePoll = 2 * time.Second

// result is what a worker returns to Run.
type result struct {
	id      string
	stop    *stopReason
	how     settling // for the hold for the environment, and Config.Limit
	counted bool     // started by start, so it counts toward Config.Limit
}

// Run works through bd ready (in a scoped run, one ticket and its descendants) with up to
// Concurrency workers at a time and returns the exit code.
func (o *Loop) Run(ctx context.Context) int {
	defer close(o.runDone) // triage counts its verdicts itself from here
	o.begin(ctx)
	// What the machine lacks would fail every ticket: the run stops before its first.
	if s := o.checkRequires(ctx, o.cfg.Requires); s != nil {
		return o.stop(s, s.Error())
	}
	if ctx.Err() != nil {
		return o.interrupted(ctx)
	}
	o.sayReadme()
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
	o.leftMu.Lock()
	o.carriedIn = true // from here, quitting at once saves the workers left (see SaveLeft)
	o.leftMu.Unlock()
	o.sayDeferredFree(ctx) // knowing what is carried over and unmerged
	defer o.startPredicting()()
	defer o.watchLoad(ctx)() // for the dashboard

	r := newRunState(ctx, o)
	defer r.machine.unregister()
	poll := time.NewTicker(readyPoll)
	defer poll.Stop()
	r.follow() // the workers carried over from the last run that closed their tickets, or work on them again
	for {
		r.takeDrainRequest()
		r.heedEnvironment()
		r.lookAround()
		r.mergedByHand()    // before checking them again
		r.recheckTickets()  // before new tickets: each is done but for its merge, and others may wait on it
		r.recheckRequires() // a merge changed them: before the next ticket starts
		r.startTickets()
		r.machine.running(len(r.inflight)) // for the other runs to count before this one next looks around
		if len(r.inflight) == 0 {
			if r.awaitFiling() || r.goOnWhenEmpty() || r.awaitAnswers() {
				continue
			}
			return r.end()
		}
		w := waits{results: r.results, joined: o.slots.joined, filing: r.filing, poll: poll.C}
		for waiting := true; waiting; {
			waiting = false
			// A verdict's hold is picked up above, a request heard, and once a hold lifts (the gate) or a
			// ticket goes to merge, freeing its slot, tickets start above.
			switch why, res := r.wait(w); why {
			case wokeResult:
				r.returned(res)
			case wokeFiled: // the rest of the scope is filed: its tickets start above
				r.filed()
			case wokePoll:
				r.polled()
			case wokeEnd:
				waiting = true // a hold came to its end and holds on, as the machine still full: nothing starts
			case wokeDone:
				return r.end()
			default:
			}
		}
	}
}

// begin notes when the run starts and Base's commit then, says how it runs, and finds from the
// workers' claude's --help whether it takes their permission mode and the tools they go without.
func (o *Loop) begin(ctx context.Context) {
	c := o.cfg
	o.started = time.Now()
	var err error // "" on failure: the reviewer reads no commits
	o.startHead, err = o.checkout.Head(ctx, c.Repo, c.Base)
	o.log.Raw("", err)
	ticketLimit := "none"
	if c.TicketLimit > 0 {
		ticketLimit = command.ShortDuration(c.TicketLimit)
	}
	term, where := c.terminalLabel(c.Terminal), c.Workspace
	if term.Where != "" {
		where = term.Where
	}
	o.info("START orchestra %s in %s on %s%s%s (done so far: %d, limit: %s, concurrent: %d, ticket limit: %s, "+
		"check timeout: %s, terminal: %s, %s: %s, agent: %s%s, worktrees: %s, MCP servers: %s)",
		c.Version, c.Repo, c.Base, ScopeLabel(c), overnightLabel(c), c.DoneSoFar, LimitLabel(c.Limit), c.Concurrency,
		ticketLimit,
		command.ShortDuration(o.checkTimeout()), term.Name, term.Tabs, where, c.Agent().Name, c.permissionLabel(),
		c.WTRoot, c.mcpLabel())
	if c.Watch != "" {
		o.info("  to see the workers' tabs: %s", c.Watch)
	}
	if c.Feature != "" {
		o.info("  feature: epic %s, talked through from: %s", c.Ticket, FeatureLine(c.Feature))
	}
	if c.Spec != "" {
		o.info("  spec: epic %s, broken down from: %s", c.Ticket, c.Spec)
	}
	o.sayMCP()
	for _, note := range c.TemplateNotes {
		o.info("  templates: %s", note)
	}
	help, err := o.claudeHelp(ctx)
	o.permission = o.permissionArgs(ctx, help, err)
	o.unusedTools = o.unusedToolArgs(help)
}

// overnightLabel is what the START line says of an overnight run after its scope, " · overnight", and
// "" of another.
func overnightLabel(c Config) string {
	if c.Overnight {
		return " · overnight"
	}
	return ""
}

// claudeHelp is the workers' claude's --help, read once as the run begins: "" with no error for
// workers of another kind, which aren't Claude Code's.
func (o *Loop) claudeHelp(ctx context.Context) (string, error) {
	switch {
	case !o.cfg.Agent().ClaudeArgs:
		return "", nil
	case o.help == nil:
		return "", errors.New("nothing to ask it with")
	}
	return o.help.Help(ctx)
}

// runState is what Run owns: only its goroutine reads or changes it. Its methods are Run's steps.
type runState struct {
	o    *Loop
	ctx  context.Context // the run's: Ctrl+C cancels it
	keep context.Context // for reopening tickets as the run holds

	// results: workers' results, buffered to twice Concurrency (the workers at work and as many
	// tickets merging) so a worker hands its result in without waiting on Run's other steps; any beyond
	// it (adoptions, rechecks, merges past the cap) wait for Run or awaitInflight, which takes every one
	// in flight
	results chan result
	// inflight: the tickets whose goroutines haven't returned, those gone to merge included (see
	// slotsTaken)
	inflight map[string]bool
	// count: the tickets dispatched but not requeued, with those done in earlier runs (Config.DoneSoFar),
	// which hold the run at Config.Limit (see setCount); the run's stops and whether it winds down are the
	// gate's (Loop.gate)
	count int
	// filing: closed once the scope is filed (Config.Filing), until when only its first ticket starts;
	// nil when there is no filing, or once it is over
	filing <-chan struct{}
	// putTo: the questions put to the maintainer in an answer talk now over (see awaitAnswers), and
	// left: those open as the run last waited on them with nothing ready, for its done line
	putTo map[string]bool
	left  []Question

	solo soloQueue // the tickets labelled SoloLabel
	// The queue last reported, its queued -1 before the first, and the solo ticket and the tickets
	// not closed with it (see reportQueue).
	queued       queue
	soloShown    SoloState
	unclosedSaid int
	fullShown    bool // the machine's runs at Config.MachineWorkers, as last reported (see machineFull)
	// unclosed: the tickets not closed that the run draws from, as pick last read them (see
	// countUnclosed); -1 before the first read.
	unclosed int

	// Scheduling by footprint: the repository's files, the reason each ready ticket was last skipped
	// and the shared edits warned about. The running tickets' footprints are the ledger's.
	files    *repoFiles
	skipSaid sayOnce
	warned   map[string]bool
	// The files git merges by union, or not, as git said in this run (mergesByUnion).
	unionMu sync.Mutex
	unions  map[string]bool
	// The files each ticket's branch changes on top of Base, as git said while it waited (branchChanges).
	branchMu sync.Mutex
	branches map[string][]string

	machine *machine // the run's registration among the machine's orchestra runs

	baseSeen string // the commit Base was on as mergedByHand last asked git of the tickets not merged
}

func newRunState(ctx context.Context, o *Loop) *runState {
	r := &runState{
		o: o, ctx: ctx, keep: context.WithoutCancel(ctx),
		results: make(chan result, 2*o.cfg.Concurrency), inflight: map[string]bool{},
		queued: queue{queued: -1}, unclosedSaid: -1, unclosed: -1,
		warned: map[string]bool{}, unions: map[string]bool{}, branches: map[string][]string{},
		machine: o.register(), filing: o.cfg.filingDone(), putTo: map[string]bool{},
		baseSeen: o.startHead, // as loadUnmerged asked
	}
	r.setCount(o.cfg.DoneSoFar)
	o.gate.register(gateMachine, r.machineEnd)
	o.shareCores(0) // for the workers taken up before Run first reads the other runs
	return r
}

// setCount sets the run's count of tickets to n, holding the run at Config.Limit: the tickets running
// finish, and merge, but no new one starts.
func (r *runState) setCount(n int) {
	r.count = n
	r.o.gate.set(gateLimit, gateHold{on: n >= r.o.cfg.Limit})
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

// addStop records s among the run's stops, on the gate, where the merges read at once that the run
// holds, and reports whether it is the first.
func (r *runState) addStop(s *stopReason) (first bool) { return r.o.gate.addStop(s) }

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

// taking reports whether the run takes new tickets: the gate is open (nothing has stopped the run,
// the maintainer hasn't asked it to wind down, Config.Limit isn't reached, it doesn't hold for the
// usage limit), but for the machine's cap on workers, which the ticket to start waits for (see
// machineFull).
func (r *runState) taking() bool { return r.o.gate.open(gateMachine) }

// wouldTake reports whether the run would take new tickets but for the usage limit (see taking).
func (r *runState) wouldTake() bool { return r.o.gate.open(gateMachine, gateUsage) }

// hear logs a request to wind down or to take tickets again, when it changes anything, and says
// whether the run winds down. The gate holds the run while it does, which the merges read at once.
func (r *runState) hear(req drainRequest) bool {
	if r.o.gate.set(gateDrain, gateHold{on: req.on, why: "the run is winding down"}) {
		r.o.emit(drainEvent(req.on, req.how, r.inflight))
		if req.on {
			r.o.dropRechecks("the run winds down")
		}
	}
	return req.on
}

// drained reports whether the maintainer asked the run to stop after the running tickets, as last heard.
func (r *runState) drained() bool { return r.o.gate.held(gateDrain) }

// takeDrainRequest hears a request waiting, if any; asked just before a ticket would start, it keeps
// that ticket from starting.
func (r *runState) takeDrainRequest() bool {
	select {
	case req := <-r.o.drainReqs:
		return r.hear(req)
	default:
		return r.drained()
	}
}

// heedEnvironment takes up the hold for the environment, once workers fail at once whichever
// ticket they have: on the gate (gateEnv), with its reason among the run's stops, and said once, before
// another ticket starts.
func (r *runState) heedEnvironment() {
	s := r.o.envStop
	if s == nil || r.o.gate.held(gateEnv) {
		return
	}
	r.o.gate.set(gateEnv, gateHold{on: true})
	r.addStop(s)
	r.sayHold(s, "")
}

// slotsTaken is how many of the Concurrency slots the tickets in flight take. A ticket whose worker
// has settled and that has gone to merge takes none while it waits for its turn, merges or has its
// worker resolve or fix its branch (see mergeSlots), so the next ticket starts meanwhile; but no more
// than Concurrency of them go without one, so that a slow merge check can't let finished tickets
// pile up without end: beyond that cap, each takes a slot until a merge ends.
func (r *runState) slotsTaken() int {
	merging := r.o.slots.goneToMerge(r.inflight)
	return len(r.inflight) - merging + max(0, merging-r.o.cfg.Concurrency)
}

// slotFree reports whether a slot is free for another ticket (see slotsTaken).
func (r *runState) slotFree() bool { return r.slotsTaken() < r.o.cfg.Concurrency }

// startTickets starts tickets while there are free slots, unless something has stopped the run or
// the maintainer asked it to wind down.
func (r *runState) startTickets() {
	for r.taking() && r.ctx.Err() == nil && r.slotFree() {
		t, q, s := r.next()
		if s != nil {
			r.addStop(s)
			r.sayHold(s, "")
			return
		}
		if t == nil {
			r.reportQueue(q)
			return // nothing ready that isn't already running, or it waits for a solo ticket
		}
		if r.machineFull() {
			q.queued++ // t waits for a slot across the machine
			r.reportQueue(q)
			return
		}
		if r.takeDrainRequest() {
			return
		}
		r.start(*t, q)
	}
}

// start hands ticket t to a worker, with the queue q behind it.
func (r *runState) start(t Ticket, q queue) {
	o, c := r.o, r.o.cfg
	r.setCount(r.count + 1)
	o.book.setParent(t.ID, t.Parent)
	how := "dispatching"
	if HasLabel(t, SoloLabel) {
		r.solo.running, how = t.ID, "dispatching solo"
	}
	if w, ok := o.asked(t.ID); ok && w.question != "" {
		o.emit(Event{Kind: EvAnswered, Ticket: t.ID, Title: t.Title, Detail: w.question + ": " + w.title, Text: fmt.Sprintf(
			"  ANSWERED: %s (%s) is answered, so %s comes back", w.question, w.title, t.ID)})
	}
	r.queued, r.soloShown, r.unclosedSaid, r.fullShown = q, r.solo.state(), r.unclosed, r.machine.full
	o.emit(Event{Kind: EvDispatch, N: r.count, Limit: c.Limit, Ticket: t.ID, Title: t.Title,
		QueueState: QueueState{Queued: q.queued, Held: q.held, Solo: r.soloShown, Unclosed: r.unclosedSaid,
			MachineFull: r.fullShown},
		Text: fmt.Sprintf("[%s] %s %s: %s", Progress(r.count, c.Limit), t.ID, how, t.Title)})
	r.startFootprint(t)
	tr := o.newTicketRun(r.ctx, t)
	r.launch(t.ID, true, &tr.how, tr.work)
}

// launch runs ticket id's worker, work, on a goroutine of its own, which hands its result to Run,
// with how its worker settled, as work sets how (nil: it has no worker to settle). The ticket runs
// from now until Run takes the result. counted says that it counts toward Config.Limit. A ticket
// whose work returns no reason to stop is no longer active; one that returns a reason stays
// listed, and so does one whose work panics: the panic is that reason, PANIC (see panicStop).
func (r *runState) launch(id string, counted bool, how *settling, work func(ctx context.Context) *stopReason) {
	r.inflight[id] = true
	o, ctx, results := r.o, r.ctx, r.results
	go func() {
		res := result{id: id, counted: counted}
		defer func() {
			if how != nil {
				res.how = *how
			}
			if p := recover(); p != nil {
				res.stop = o.panicStop(id, p)
			} else if res.stop == nil {
				o.clearActive(id)
			}
			results <- res
		}()
		res.stop = work(ctx)
	}()
}

// follow adopts the asked tickets whose workers claimed them again or closed them in their tabs
// (see followAsked), and merges those set aside whose workers, left in their tabs, closed them there
// (see mergeLate), and reports whether it took up any. Their workers are at work already, or done,
// so they count as running at once, even beyond Concurrency. Once something has stopped the run,
// they are left to leaveAsked, and to their UnmergedLabel.
func (r *runState) follow() bool {
	o := r.o
	if o.gate.firstStop() != nil || r.ctx.Err() != nil {
		return false
	}
	late := r.mergeLate()
	adopt, s := r.followAsked()
	for _, a := range adopt {
		if HasLabel(a.t, SoloLabel) {
			r.solo.running = a.t.ID // it runs alone from now on, as one dispatched does
		}
		tr := o.newTicketRun(r.ctx, a.t)
		r.launch(a.t.ID, false, &tr.how, func(ctx context.Context) *stopReason {
			return tr.adoptAsked(ctx, a.w, a.resume)
		})
	}
	if s != nil {
		r.addStop(s)
	}
	return late || len(adopt) > 0
}

// goOnWhenEmpty is Run's step once nothing runs, and reports whether the run goes on: asked tickets were
// adopted, the usage limit the run held for lifted (see waitOutLimit), or the run held for the
// environment and a probe found that commands run again.
func (r *runState) goOnWhenEmpty() bool {
	// Before the run ends, the asked tickets are read once more.
	if r.follow() {
		return true
	}
	if r.wouldTake() && r.o.usage.holds() {
		return r.waitOutLimit()
	}
	// Held for the environment, the run may probe the machine and take tickets again.
	s := r.o.gate.onlyStop()
	if s == nil {
		return false
	}
	s = r.probeEnvironment(s)
	r.o.gate.replaceStop(s)
	return s == nil
}

// returned takes a worker's result: its ticket no longer runs, and the reason it stopped the run
// for, if any, holds the run.
func (r *runState) returned(res result) {
	o := r.o
	delete(r.inflight, res.id)
	o.slots.doneMerging(res.id)
	o.stoppedBy(res.id, res.stop)
	r.endFootprint(res.id)
	o.countSettling(r.keep, res.id, res.how)
	if res.counted && (res.how == requeued || res.how == setupFailed) {
		r.setCount(r.count - 1) // it started too early, or not at all, and counts once it starts again
	}
	if res.id == r.solo.running {
		r.solo.running = ""
	}
	if res.stop == nil {
		o.parentDone(r.ctx, res.id, r.inflight)
	}
	if res.stop == nil || res.stop == errInterrupted {
		return
	}
	// Every reason is shown as it arrives. The first decides the exit code; the final line gives it
	// and then the others.
	if first := r.addStop(res.stop.over(res.id)); len(r.inflight) > 0 || !first {
		o.emit(Event{Kind: EvHold, Ticket: res.id, Blocked: res.stop.blocked, Text: holdLine(res.stop, len(r.inflight))})
	}
}

// polled is Run's step on each poll: it follows the asked tickets and those left open, warns of
// edits the workers share and, with every slot taken, brings the queue count up to date.
func (r *runState) polled() {
	o := r.o
	r.follow()
	if o.footprintOn() && len(r.inflight) > 1 {
		r.readEdits() // warns when two workers edit the same file
	}
	// With a free slot, Run reads bd ready again as it starts tickets. With none, the queue count is
	// brought up to date; a failed read waits for the next poll, as nothing depends on it.
	if r.taking() && !r.slotFree() {
		if t, q, err := r.pick(false); err == nil {
			if t != nil {
				q.queued++
			}
			r.reportQueue(q)
		}
	}
}

// reportQueue tells the dashboard how many ready tickets wait for a slot and how many are held back
// (q), which solo ticket runs or is next, how many tickets aren't closed and whether the run holds for
// the machine's cap on workers (see machineFull), when any of that has changed.
func (r *runState) reportQueue(q queue) {
	solo, unclosed, full := r.solo.state(), r.unclosed, r.machine.full
	if q == r.queued && solo == r.soloShown && unclosed == r.unclosedSaid && full == r.fullShown {
		return
	}
	r.queued, r.soloShown, r.unclosedSaid, r.fullShown = q, solo, unclosed, full
	r.o.emit(Event{Kind: EvQueue,
		QueueState: QueueState{Queued: q.queued, Held: q.held, Solo: solo, Unclosed: unclosed, MachineFull: full}})
}

// end ends the run, once nothing runs or Ctrl+C came, and returns its exit code. It leaves behind
// the workers that stopped the run, if any, and those waiting on a question, and says how the run
// ended. A run interrupted only once it has come to its end, as it leaves its workers behind, ends as
// it came to: Ctrl+C then cuts short nothing it would report.
func (r *runState) end() int {
	o, c := r.o, r.o.cfg
	interrupted := r.ctx.Err() != nil
	o.awaitInflight(r.keep, r.results, r.inflight) // after Ctrl+C: otherwise none is in flight
	if !interrupted {
		r.mergedByHand() // for the end line and the reviewer
	}
	o.leaveBehind(r.ctx)
	var end string
	switch {
	case interrupted:
		return o.interrupted(r.ctx)
	case o.gate.firstStop() != nil:
		o.dropRechecks("the run stopped")
		return o.stop(o.gate.firstStop(), o.gate.stopLine())
	case r.drained():
		end = fmt.Sprintf("DRAINED after %d tickets", r.count)
	case r.count >= c.Limit:
		end = fmt.Sprintf("LIMIT_REACHED at %d tickets", r.count)
	default:
		end = fmt.Sprintf("READY_EMPTY after %d tickets", r.count) + questionsEnd(r.left)
	}
	o.emit(Event{Kind: EvDone, N: r.count, Limit: c.Limit, Text: end + o.setupWaits() + o.endScope(r.keep)})
	return ExitOK
}

// awaitInflight waits after Ctrl+C for the workers in flight to return, so what follows (triage, the run
// report) reads a loop that has stopped changing and no worker is left behind. Each command a
// worker runs stops with the run or at its time limit, and only what must finish once begun (a
// merge under way, the notes after it) goes on, so they return within seconds, or within a
// command's time limit when one hangs. Those not back within settleSay are named, so the terminal
// doesn't sit silent meanwhile. A worker that settled still counts toward the hold.
func (o *Loop) awaitInflight(ctx context.Context, results <-chan result, inflight map[string]bool) {
	slow := time.NewTimer(settleSay)
	defer slow.Stop()
	for len(inflight) > 0 {
		select {
		case r := <-results:
			delete(inflight, r.id)
			o.slots.doneMerging(r.id)
			o.stoppedBy(r.id, r.stop)
			o.countSettling(ctx, r.id, r.how)
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
// are fast-forwarded into it; each read of it waits for a git write under way (repoMu), as a merge's
// fast-forward, and lets go between reads, so git failing doesn't hold up the merges.
func (r *runState) next() (*Ticket, queue, *stopReason) {
	o, ctx := r.o, r.ctx
	s := o.checkoutUnready(ctx, "", &o.repoMu)
	if ctx.Err() != nil {
		return nil, queue{}, nil // Ctrl+C: nothing starts, and a read it cut short says nothing
	}
	if s != nil && len(r.inflight) >= o.cfg.Concurrency && o.slots.goneToMerge(r.inflight) > 0 {
		// The slot is free only as tickets merge (see slotsTaken), and their merges meet the checkout as
		// it is: they stop the run, saying which ticket it keeps from merging. Nothing starts meanwhile,
		// and the queue stays as last reported.
		return nil, r.queued, nil
	}
	if s != nil {
		return nil, queue{}, s
	}
	t, q, err := r.pickTries(r.slotFree())
	if ctx.Err() != nil {
		return nil, queue{}, nil
	}
	if err != nil {
		what := "'bd ready --json'"
		if errors.As(err, new(listUnreadableError)) {
			what = "'bd list --json'"
		}
		return nil, queue{}, halt(ExitTool, stopReadyUnreadable, ": could not read %s%s", what, because(err)).causedBy(err)
	}
	return t, q, nil
}

// readyTries is how many times next reads the queue while bd fails before it stops the run.
const readyTries = 3

// pickTries is pick, tried up to readyTries times a poll apart while bd fails, as when a worker's bd
// (a claim, a close) holds the database: bd failing once or twice doesn't stop the run. It logs each
// failure it tries again after.
func (r *runState) pickTries(slot bool) (*Ticket, queue, error) {
	o, ctx := r.o, r.ctx
	for try := 1; ; try++ {
		t, q, err := r.pick(slot)
		if err == nil || try == readyTries || ctx.Err() != nil || !sleep(ctx, o.pollEvery()) {
			return t, q, err
		}
		o.log.Raw("", fmt.Errorf("could not read the queue (try %d of %d), reading it again: %w", try, readyTries, err))
	}
}

// soloQueue is the run's tickets labelled SoloLabel, Run's own: the one running, the one next in
// line waiting for the running tickets to finish, the one first in line at the last pick, whether
// or not a slot was free (no recheck takes a slot ahead of it), and the wait last logged. What the
// dashboard was last told is runState's.
type soloQueue struct {
	running, next, first, said string
}

// state is the solo ticket running, or else the one next in line.
func (q *soloQueue) state() SoloState {
	switch {
	case q.running != "":
		return SoloState{Ticket: q.running}
	case q.next != "":
		return SoloState{Ticket: q.next, Next: true}
	}
	return SoloState{}
}

// update takes a pick's outcome: next, the solo ticket first in line that holds back the others
// (see pickNext), whether a slot was free, the ticket to start, if any, and how many others are
// ready. It returns why tickets wait for a solo ticket when that has changed since it was last
// said, or "". A solo ticket holds something back only when a slot is free; with every slot taken
// (with one worker, whenever a worker is at work) the tickets wait for a slot as they would anyway.
// It still goes ahead of a recheck when a slot frees, which recheckTickets decides before the next
// pick.
func (q *soloQueue) update(next string, slot bool, t *Ticket, queued int) (why string) {
	q.first = next
	if !slot {
		next = ""
	}
	q.next = next
	switch {
	case slot && t == nil && queued > 0 && q.running != "":
		why = fmt.Sprintf("waiting for solo ticket %s to finish", q.running)
	case next != "":
		why = fmt.Sprintf(
			"solo ticket %s is next: no new tickets start until the running ones finish, then it runs alone", next)
	}
	if why == q.said {
		return ""
	}
	q.said = why
	return why
}

// pick reads bd ready (in a scoped run, the scope's part of it) and returns the highest-priority
// ticket that can start, with how many others could and how many are held back, by what holds
// them. With a free slot (slot, as the caller found it: a ticket going to merge may free one
// meanwhile) and none to start, those that could wait for a solo ticket, and count as held back. It
// says once why they wait, keeping the solo tickets in runState.solo.
func (r *runState) pick(slot bool) (*Ticket, queue, error) {
	o, ctx, running, solo := r.o, r.ctx, r.inflight, &r.solo
	ready, err := o.tickets.Ready(ctx, o.cfg.Ticket)
	if err != nil {
		return nil, queue{}, err
	}
	ready = r.filingOnly(ready)
	open, err := o.tickets.Unclosed(ctx)
	if err != nil {
		return nil, queue{}, listUnreadableError{err}
	}
	r.countUnclosed(ctx, open)
	parents := o.parentsOpen(open, running)
	// A ticket set aside in this run stays out of it, even if bd still lists it as ready (a defer
	// that failed, say); otherwise it would be dispatched again at once, in a new tab each time. A
	// ticket waiting on a question is the exception: bd keeps it out of ready until the question is
	// answered, and then it comes back.
	skip := map[string]bool{}
	for _, id := range o.book.keptOut() {
		skip[id] = true
	}
	for id := range running {
		skip[id] = true
	}
	// A ticket whose worker the last run left, kept for a later run, is that worker's: its question
	// answered, it would otherwise get a second worker in the same worktree.
	for _, id := range o.keptOutIDs() {
		skip[id] = true
	}
	// A ticket whose ID isn't a plain name is set aside before its ID is made a path or a branch.
	o.setAsideBadIDs(context.WithoutCancel(ctx), ready, skip)
	// With a free slot, a ticket whose footprint overlaps a running ticket's is skipped for the next
	// one that doesn't; with none free, the tickets wait for a slot anyway.
	overlaps := func(Ticket) bool { return false }
	if slot && o.footprintOn() {
		r.readFiles(ctx)
		if len(running) > 0 {
			r.readEdits()
			overlaps = func(t Ticket) bool { return r.overlapsRunning(t, running) }
		}
	}
	if o.footprintOn() {
		r.queuePredictions(ready, skip)
	}
	held := func(t Ticket) holdKind {
		// A ticket whose worktree's setup failed waits for what may bring what the setup needs: taken
		// again at once, its setup would fail again (see leaveSetupFailed).
		if _, failed := o.failedSetupOf(t.ID); failed {
			return heldSetup
		}
		if k := o.held(ctx, t, running, parents); k != notHeld {
			return k
		}
		if overlaps(t) {
			return heldOverlap
		}
		return notHeld
	}
	o.mu.Lock()
	o.picks++
	o.mu.Unlock()
	t, q, next := pickNext(ready, skip, held, len(running), solo.running)
	// The ticket to start is held on its links as they are now: one kept from an earlier pick may
	// lack a discovered-from link added since. Read again, the links hold it or the pick stands.
	for slot && t != nil && o.staleLinks(t.ID) {
		t, q, next = pickNext(ready, skip, held, len(running), solo.running)
	}
	if why := solo.update(next, slot, t, q.queued); why != "" {
		o.info("  %s", why)
	}
	if slot && t == nil {
		q.held.Solo, q.queued = q.queued, 0 // with a slot free, only a solo ticket keeps them waiting
	}
	return t, q, nil
}

// checkoutUnready returns a reason to stop when the main checkout has uncommitted changes or is
// not on Base, or nil. A fast-forward there moves whichever branch is checked out, so it must be
// Base. leftForReview, when set, says what is left for review: a finished ticket, which the reason then
// blocks from merging. lock is held for each read of the checkout (see readCheckout).
func (o *Loop) checkoutUnready(ctx context.Context, leftForReview string, lock sync.Locker) *stopReason {
	c := o.cfg
	stop := func(s *stopReason, why string) *stopReason {
		if leftForReview == "" {
			return s
		}
		return s.blocks(why)
	}
	dirty, branch, err := o.readCheckout(ctx, lock)
	if err != nil {
		return stop(halt(ExitTool, stopGitFailed,
			": could not read the state of %s%s; stopping%s", c.Repo, because(err), leftForReview).causedBy(err), "git failed")
	}
	if dirty != "" {
		return stop(halt(ExitDirty, stopDirtyTree,
			": uncommitted changes in %s; stopping%s. Inspect with: git status", c.Repo, leftForReview),
			"main checkout has uncommitted changes")
	}
	if !isBranch(branch, c.Base) {
		return stop(halt(ExitDirty, stopDirtyTree,
			": %s is no longer on %s; stopping%s. Check it out again to continue.", c.Repo, c.Base, leftForReview),
			"main checkout is not on "+c.Base)
	}
	return nil
}

// gitTries is how many times readCheckout asks git before giving up: a git call can fail for a
// moment on a busy machine, and that says nothing about the checkout.
const gitTries = 3

// readCheckout returns the main checkout's uncommitted changes and its branch, or git's error. It
// holds lock for each try and lets go of it while it waits to try again: repoMu for a caller that
// doesn't hold it, alreadyLocked for one that does.
func (o *Loop) readCheckout(ctx context.Context, lock sync.Locker) (dirty, branch string, err error) {
	c := o.cfg
	for try := 1; ; try++ {
		lock.Lock()
		dirty, branch, err = o.checkout.DirtyTreeAndBranch(ctx, c.Repo)
		lock.Unlock()
		if err == nil {
			return dirty, branch, nil
		}
		if try == gitTries || ctx.Err() != nil || !sleep(ctx, o.pollEvery()) {
			return "", "", err
		}
		o.log.Raw("", err) // tried again; only the error returned is the caller's to report
	}
}

// isBranch reports whether branch, as git status names it, is base, as 'git symbolic-ref --short' named
// it at the start: the branch's name, or with heads/ or refs/heads/ before it where a tag or another
// ref has that name too.
func isBranch(branch, base string) bool {
	return branch != "" && (base == branch || base == "heads/"+branch || base == "refs/heads/"+branch)
}

// alreadyLocked is the lock readCheckout takes for a caller that holds repoMu already.
type alreadyLocked struct{}

func (alreadyLocked) Lock()   {}
func (alreadyLocked) Unlock() {}

// pickNext returns the first ticket (ready is in priority order) that isn't skipped or held, and
// in q how many other ready tickets aren't either (queued), and how many not skipped are held, by
// what holds them (held returns it). A ticket labelled SoloLabel runs alone: while solo (the one
// running, or "") runs nothing starts, and one first in line starts only once none of the running
// tickets are left, holding back the tickets behind it until then so it isn't starved; next names
// it. With no ticket to start, queued is how many wait.
func pickNext(
	ready []Ticket, skip map[string]bool, held func(Ticket) holdKind, running int, solo string,
) (t *Ticket, q queue, next string) {
	var free []Ticket
	for _, t := range ready {
		if skip[t.ID] {
			continue
		}
		if k := held(t); k != notHeld {
			q.held.add(k)
			continue
		}
		free = append(free, t)
	}
	switch {
	case len(free) == 0:
		return nil, q, ""
	case solo != "":
		q.queued = len(free)
		return nil, q, ""
	case HasLabel(free[0], SoloLabel) && running > 0:
		q.queued = len(free)
		return nil, q, free[0].ID
	}
	q.queued = len(free) - 1
	return &free[0], q, ""
}
