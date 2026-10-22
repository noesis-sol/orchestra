package dispatch

import (
	"fmt"
	"slices"
	"strconv"
	"time"
)

// Kind is what an Event reports.
type Kind int

// The kinds of event. Each has a name in the event stream (kindNames): give a new one its own.
const (
	EvInfo      Kind = iota // progress detail (start, worktree)
	EvDispatch              // a ticket was picked up
	EvClosed                // a ticket was completed and merged, or closed with no change of its own to merge
	EvDeferred              // a ticket was set aside
	EvWarn                  // something needs review, but the loop continues; Aside if a ticket is left for it
	EvStop                  // the loop stopped and needs attention
	EvDone                  // the loop finished normally
	EvTriage                // the triage organ's verdict on a deferred ticket
	EvAsked                 // a ticket waits on the maintainer's answer to a question
	EvWaits                 // a ticket started before a ticket blocking it merged: back in the queue until it does
	EvReopened              // a ticket set aside was put back in the queue: no longer set aside
	EvRequeued              // a ticket was put back in the queue, whatever put it there (Detail: why); not logged
	EvHold                  // something stopped the run; no new tickets while the running ones finish
	EvDrain                 // the maintainer asked to stop after the running tickets
	EvResume                // the maintainer took that back
	EvQueue                 // the number of ready tickets waiting changed; for the dashboard, not logged
	EvProbed                // a probe found the machine working after an environment hold: tickets start again
	EvLimited               // a worker or organ met the account's usage limit: no new tickets until it resets (Reset)
	EvLimitLift             // the usage limit reset, or a worker went on past it: tickets start again
	EvShared                // another orchestra run on the machine started, or ended (Shared: those going)
	EvMachine               // a free slot waits: the workers across the machine's runs are at Config.MachineWorkers
	EvAnswered              // an asked ticket's question was answered: it comes back, dispatched next
	EvFullCheck             // the full check (check_full) at the end of the run: Detail says how it went
)

// kindNames are the kinds' names in the event stream, which programs read: keep them as they are.
var kindNames = [...]string{
	EvInfo: "info", EvDispatch: "dispatch", EvClosed: "closed", EvDeferred: "deferred", EvWarn: "warn",
	EvStop: "stop", EvDone: "done", EvTriage: "triage", EvAsked: "asked", EvWaits: "waits",
	EvReopened: "reopened", EvRequeued: "requeued", EvHold: "hold",
	EvDrain: "drain", EvResume: "resume", EvQueue: "queue", EvProbed: "probed",
	EvLimited: "usage_limit", EvLimitLift: "usage_limit_lifted", EvShared: "shared_machine",
	EvMachine: "machine_hold", EvAnswered: "answered",
	EvFullCheck: "full_check",
}

// String is k's name in the event stream: info, dispatch, closed and so on.
func (k Kind) String() string {
	if k >= 0 && int(k) < len(kindNames) && kindNames[k] != "" {
		return kindNames[k]
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// HasLine reports whether an event of kind k has a line in the log: EvQueue and EvRequeued, for the
// dashboard and the event stream, have none.
func (k Kind) HasLine() bool { return k != EvQueue && k != EvRequeued }

// Event is one thing that happened in the run, for the log, the event stream and the sink.
type Event struct {
	Time   time.Time
	Kind   Kind
	N      int // EvDispatch: the ticket's number in the run; EvDone: the tickets in all, as Text counts them
	Limit  int // EvDispatch and EvDone: the ticket limit, NoLimit for none
	Ticket string
	// Title: the ticket's title, for EvDispatch, EvClosed, EvDeferred, EvAsked, EvWaits and
	// EvAnswered; the verdict's summary for EvTriage.
	Title      string
	QueueState // EvDispatch and EvQueue: the queue as it stands
	// Shared: for EvShared, how many other orchestra runs are going on the machine.
	Shared int
	// Detail: a short suffix for EvClosed and EvDeferred, and for an EvWarn that sets its ticket
	// aside, why; for an EvWarn about Base as the run starts (see fetchBase), the word its line starts
	// with (BASE_DIVERGED, …); for EvAsked and EvAnswered, the question's ID and title ("Q: title"); for EvWaits,
	// the tickets it waits for, as in "A, B"; for EvReopened and EvRequeued, why it is back in the queue; for
	// EvStop, what stopped the run, the word its line starts with (PAUSED, INTERRUPTED, …).
	Detail string
	Text   string // the full line written to the log file
	// Reset: for EvLimited, when the limit resets; zero when Claude Code didn't say, or said it in
	// words not read.
	Reset time.Time
	// Closed and SetAside: for EvDone, the tickets closed in the run (merged, or with nothing to
	// merge), as their events said, and those set aside (deferred, or left for review), as the ticket
	// book has them (see ticketBook.asideCount).
	Closed, SetAside int
	// Aside: an EvWarn that leaves Ticket set aside for review, out of this run (CHECKS_FAILED,
	// DEFER_FAILED, …), not one about a ticket still running or already deferred.
	Aside bool
	// Times: for an event that ends Ticket's part in the run (see endsPart), how long it spent in each
	// phase since the run first took it up; nil when the run didn't take it up.
	Times *PhaseTimes
	// Tokens: for an event that ends Ticket's part in the run, what its workers' sessions used since the run
	// took it up, by model, as their transcripts say; nil when they can't say (see ticketTokens).
	Tokens Tokens
	// Blocked: Ticket's work is done, but it can't merge until the maintainer acts; why, in a few
	// words ("main checkout has uncommitted changes", "merge conflict in a.go"). Set on the EvHold or
	// EvStop for a DIRTY_TREE, GIT_FAILED or MERGE_FAILED met as it merged, and on an EvWarn for its
	// MERGE_CONFLICT; not on CHECKS_FAILED, CLOSED_WITHOUT_COMMIT or CLOSED_UNCOMMITTED, where the
	// work itself needs a look.
	Blocked string
	// Suite and Output: for an EvFullCheck that failed, the suite it failed in (the last line its
	// output starts with project.SuiteMarker, or else the check itself; "" when the setup before it
	// failed) and where its whole output is.
	Suite, Output string
	// Ran: for an EvFullCheck that ran, how: its commit, how long it took and how it ended.
	Ran *CheckRun
}

// QueueState is the queue as EvDispatch and EvQueue report it, for the dashboard.
type QueueState struct {
	Queued int       // ready tickets waiting for a slot
	Held   HeldBack  // ready tickets held back, waiting for something besides a slot
	Solo   SoloState // the solo ticket running or next, if any
	// Unclosed: the tickets not closed that the run draws from (in a scoped run, the scope's); -1
	// before the run could read them.
	Unclosed int
	// MachineFull: a free slot waits while the workers across the machine's orchestra runs are at
	// Config.MachineWorkers (see EvMachine).
	MachineFull bool
}

// Status describes a ticket being worked on. Gone removes it from the display.
type Status struct {
	Ticket   string
	Title    string
	Tab      string
	Started  time.Time
	Agent    AgentState // as Herdr last reported it; "" before the first read or when it failed
	Activity string     // the worker's latest action line
	Doing    Doing      // what a working worker is doing, from its reports: testing, editing, reading, subagent or ""
	// Permission: its hooks say it waits on a permission prompt, whatever Herdr shows (idle, as
	// often as blocked)
	Permission bool
	// Phase: where its work is, which the loop sets (see enter): its worker at it, handed back to it, or
	// in the merge queue. Resolving, Fixing and Merge say the same for the sink, and follow it.
	Phase Phase
	// Resolving: its branch's rebase stopped on conflicts with Base and was handed back to its
	// worker, and is left in progress until the worker finishes it.
	Resolving bool
	Fixing    bool // its check failed on its rebased branch and was handed back to its worker to fix
	// Merge: its worker done, where its work is in the merge queue: MergeQueued, Place in line (1: next),
	// or MergeRunning; MergeNone while its worker works, a hand-back included.
	Merge      MergeStage
	Place      int
	Unreadable bool // the last read of Agent failed
	Gone       bool
	// mergeNote is what it shows as it merges, in place of the merge queue's own words: a ticket
	// checked again says so (see recheck and showMerge).
	mergeNote string
}

// Phase is where a running ticket's work is: its worker at it, handed back to it to resolve its
// rebase or fix its check, or its worker done with it, in the merge queue.
type Phase int

// The phases of a running ticket, in the order a ticket that merges goes through them; a hand-back
// takes it from the queue back to its worker, and it joins the queue again after.
const (
	PhaseWorking   Phase = iota // its worker is at it
	PhaseResolving              // its rebase stopped on conflicts, handed back to its worker
	PhaseFixing                 // its check failed on its rebased branch, handed back to its worker
	PhaseQueued                 // waiting for its turn to merge, at Place in line
	PhaseMerging                // its merge is under way
)

// enter puts st in phase p, at place in line when p is PhaseQueued, and sets the fields that say the
// same for the sink to match.
func (st *Status) enter(p Phase, place int) {
	st.Phase, st.Place = p, 0
	st.Resolving, st.Fixing, st.Merge = p == PhaseResolving, p == PhaseFixing, MergeNone
	switch p {
	case PhaseQueued:
		st.Merge, st.Place = MergeQueued, place
	case PhaseMerging:
		st.Merge = MergeRunning
	}
}

// handedBack reports whether st's worker has its rebase to resolve or its check to fix.
func (st Status) handedBack() bool { return st.Phase == PhaseResolving || st.Phase == PhaseFixing }

// Sink receives events and live status; the terminal UI and the plain printer implement it.
type Sink interface {
	Event(Event)
	Status(Status)
}

func (o *Loop) emit(ev Event) {
	var tokens Tokens
	if endsPart(ev) {
		tokens = o.ticketTokens(ev.Ticket) // read before the lock: the transcripts may be long
	}
	o.emitMu.Lock()
	defer o.emitMu.Unlock()
	ev.Time = time.Now()
	if endsPart(ev) {
		ev.Times = o.clocks.end(ev.Ticket, ev.Time)
		if ev.Times != nil { // else another record ended its part as these were read
			ev.Tokens = tokens
		}
	}
	o.tally(&ev)
	if ev.Kind.HasLine() {
		o.log.Line(ev.Time, ev.Text)
	}
	o.log.Notify(Notice(ev))
	o.log.Record(ev)
	o.sinkMu.Lock()
	defer o.sinkMu.Unlock()
	if ev.Kind == EvStop || ev.Kind == EvDone {
		o.final = ev.Text
	}
	o.sink.Event(ev)
}

// tally counts the tickets closed in the run, as their events say, and gives the run's EvDone that
// count and the ticket book's of those set aside (see asideCount).
func (o *Loop) tally(ev *Event) {
	var aside int
	if ev.Kind == EvDone {
		aside = o.book.asideCount()
	}
	o.sinkMu.Lock()
	defer o.sinkMu.Unlock()
	switch ev.Kind {
	case EvClosed:
		o.closedN++
		if !slices.Contains(o.closedIDs, ev.Ticket) {
			o.closedIDs = append(o.closedIDs, ev.Ticket)
		}
	case EvDone:
		ev.Closed, ev.SetAside = o.closedN, aside
	}
}

// closedInRun returns the tickets closed in the run, merged or with nothing to merge, in the order
// they closed.
func (o *Loop) closedInRun() []string {
	o.sinkMu.Lock()
	defer o.sinkMu.Unlock()
	return slices.Clone(o.closedIDs)
}

func (o *Loop) status(st Status) {
	o.sinkMu.Lock()
	defer o.sinkMu.Unlock()
	o.sink.Status(st)
}

// SetSink switches output, e.g. to plain lines once the terminal view has closed.
func (o *Loop) SetSink(s Sink) {
	o.sinkMu.Lock()
	defer o.sinkMu.Unlock()
	o.sink = s
}

func (o *Loop) info(format string, a ...any) {
	o.emit(Event{Kind: EvInfo, Text: fmt.Sprintf(format, a...)})
}

// stop ends the run for s with text, its line, and returns the code orchestra exits with.
func (o *Loop) stop(s *stopReason, text string) int {
	o.emit(Event{Kind: EvStop, Ticket: s.ticket, Detail: string(s.kind), Blocked: s.blocked, Text: text})
	return s.code
}
