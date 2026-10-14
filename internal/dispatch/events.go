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
	EvHold                  // something stopped the run; no new tickets while the running ones finish
	EvDrain                 // the maintainer asked to stop after the running tickets
	EvResume                // the maintainer took that back
	EvQueue                 // the number of ready tickets waiting changed; for the dashboard, not logged
	EvProbed                // a probe found the machine working after an environment hold: tickets start again
	EvAnswered              // an asked ticket's question was answered: it comes back, dispatched next
	EvFullCheck             // the full check (check_full) at the end of the run: Detail says how it went
)

// kindNames are the kinds' names in the event stream, which programs read: keep them as they are.
var kindNames = [...]string{
	EvInfo: "info", EvDispatch: "dispatch", EvClosed: "closed", EvDeferred: "deferred", EvWarn: "warn",
	EvStop: "stop", EvDone: "done", EvTriage: "triage", EvAsked: "asked", EvWaits: "waits",
	EvReopened: "reopened", EvHold: "hold",
	EvDrain: "drain", EvResume: "resume", EvQueue: "queue", EvProbed: "probed", EvAnswered: "answered",
	EvFullCheck: "full_check",
}

// String is k's name in the event stream: info, dispatch, closed and so on.
func (k Kind) String() string {
	if k >= 0 && int(k) < len(kindNames) && kindNames[k] != "" {
		return kindNames[k]
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// Event is one thing that happened in the run, for the log, the event stream and the sink.
type Event struct {
	Time   time.Time
	Kind   Kind
	N      int // EvDispatch: the ticket's number in the run; EvDone: the tickets in all, as Text counts them
	Limit  int // EvDispatch and EvDone: the ticket limit
	Ticket string
	// Title: the ticket's title, for EvDispatch, EvClosed, EvDeferred, EvAsked, EvWaits and
	// EvAnswered; the verdict's summary for EvTriage.
	Title  string
	Queued int       // EvDispatch and EvQueue: ready tickets waiting for a slot
	Held   HeldBack  // EvDispatch and EvQueue: ready tickets held back, waiting for something besides a slot
	Solo   SoloState // EvDispatch and EvQueue: the solo ticket running or next, if any
	// Unclosed: for EvDispatch and EvQueue, the tickets not closed that the run draws from (in a
	// scoped run, the scope's); -1 before the run could read them.
	Unclosed int
	// Detail: a short suffix for EvClosed and EvDeferred, and for an EvWarn that sets its ticket
	// aside, why; for EvAsked and EvAnswered, the question's ID and title ("Q: title"); for EvWaits,
	// the tickets it waits for, as in "A, B"; for EvReopened, why it is back in the queue; for
	// EvStop, what stopped the run, the word its line starts with (PAUSED, INTERRUPTED, …).
	Detail string
	Text   string // the full line written to the log file
	// Closed and SetAside: for EvDone, the tickets closed in the run (merged, or with nothing to
	// merge), and those set aside (deferred, or left for review), as their events said.
	Closed, SetAside int
	// Aside: an EvWarn that leaves Ticket set aside for review, out of this run (CHECKS_FAILED,
	// DEFER_FAILED, …), not one about a ticket still running or already deferred.
	Aside bool
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
}

// Sink receives events and live status; the terminal UI and the plain printer implement it.
type Sink interface {
	Event(Event)
	Status(Status)
}

func (o *Loop) emit(ev Event) {
	ev.Time = time.Now()
	o.tally(&ev)
	if ev.Kind != EvQueue {
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

// tally counts the tickets closed and set aside in the run, as their events say, and gives the
// counts to the run's EvDone. A ticket set aside counts once, however many times it is (its check
// failing again, say), and no longer once it merges (see recheck) or is back in the queue
// (EvReopened).
func (o *Loop) tally(ev *Event) {
	o.sinkMu.Lock()
	defer o.sinkMu.Unlock()
	switch {
	case ev.Kind == EvClosed:
		o.closedN++
		if !slices.Contains(o.closedIDs, ev.Ticket) {
			o.closedIDs = append(o.closedIDs, ev.Ticket)
		}
		o.uncount(ev.Ticket)
	case ev.Kind == EvReopened:
		o.uncount(ev.Ticket)
	case ev.Kind == EvDeferred, ev.Kind == EvWarn && ev.Aside && ev.Ticket != "":
		if o.counted[ev.Ticket] {
			return
		}
		o.counted[ev.Ticket] = true
		o.asideN++
	case ev.Kind == EvDone:
		ev.Closed, ev.SetAside = o.closedN, o.asideN
	}
}

// uncount takes ticket id out of those asideN counts, if it is there. Its caller holds sinkMu.
func (o *Loop) uncount(id string) {
	if o.counted[id] {
		o.asideN--
		delete(o.counted, id)
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
