package tui

import (
	"slices"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// What the dashboard's messages do to its state: the keys pressed (press), the loop's events (apply)
// and its workers' statuses (applyStatus). None of them calls out: Update does what press asks.

// rowEvent is an event that only sets its ticket's row: to state, with a note from the event's Detail.
type rowEvent struct {
	state rowState
	says  string                     // what the event's line says of its ticket: completed, deferred, …
	note  func(detail string) string // the row's note
	quote func(detail string) string // what the event's line says after, if not the note
}

// line is what the event's line says after the ticket, from its Detail.
func (e rowEvent) line(detail string) string {
	if e.quote != nil {
		return e.quote(detail)
	}
	return e.note(detail)
}

func same(detail string) string { return detail }

// waitsNote is an EvWaits ticket's note: the tickets it waits for (Detail, "A, B") to merge.
func waitsNote(detail string) string { return "for " + detail + " to merge" }

// reopenedNote is an EvReopened ticket's note: why it is back in the queue (Detail).
func reopenedNote(detail string) string { return "back in the queue: " + detail }

// rowEvents are the events that only set their ticket's row, by kind. The dashboard counts each
// EvClosed as well.
var rowEvents = map[dispatch.Kind]rowEvent{
	dispatch.EvClosed:   {state: rowDone, says: "completed", note: same},
	dispatch.EvDeferred: {state: rowDeferred, says: "deferred", note: same},
	dispatch.EvAsked: {state: rowAsked, says: "needs your answer", quote: same,
		note: func(q string) string { return "answer " + q }},
	// Back in the queue: its row shows it working again once it is dispatched.
	dispatch.EvWaits: {state: rowWaiting, says: "waits", note: waitsNote},
	// Set aside, now back in the queue: no longer counted set aside.
	dispatch.EvReopened: {state: rowWaiting, says: "reopened", note: reopenedNote},
}

// keyAction is what a key asks for beyond the dashboard's own state, which Update does: to quit the
// run, to turn the wind-down on or off, or to switch to a worker's tab.
type keyAction struct {
	quit     bool
	drain    *bool  // the wind-down to ask the loop for: on, or off to go on; nil for neither
	focusTab string // the tab to switch to; "" for none
}

// press takes in key and says what Update is to do for it.
func (m *Dashboard) press(key string) keyAction {
	switch {
	case key == "ctrl+c": // at any time, the question open or not
		m.interrupted, m.quitting = true, true
		return keyAction{quit: true}
	case m.asking && key == "y":
		m.asking = false
		on := !m.draining
		m.setDraining(on)
		return keyAction{drain: &on}
	case m.asking && (key == "n" || key == "esc"):
		m.asking = false
	case !m.asking && len(key) == 1 && key >= "1" && key <= "9":
		return keyAction{focusTab: m.tab(int(key[0] - '0'))}
	case key == "s" && (m.draining || !m.stopping): // a run already stopping has nothing to drain
		m.asking = true
	}
	return keyAction{}
}

// applyStatus takes in a worker's status: it runs, or with Gone it no longer does.
func (m *Dashboard) applyStatus(st dispatch.Status) {
	if st.Gone {
		delete(m.active, st.Ticket)
		return
	}
	if m.active == nil {
		m.active = map[string]dispatch.Status{}
	}
	if m.draining && !slices.Contains(m.drainFrom, st.Ticket) { // its status late, or one taken back
		m.drainFrom = append(m.drainFrom, st.Ticket)
	}
	m.active[st.Ticket] = st
}

// apply takes in one of the loop's events and reports whether the dashboard quits on it. Events
// update the dashboard in place; nothing is printed above it. The full lines are in the log file.
// Those in after it has asked to quit, before the program exits (an organ's verdict, a warning),
// would go with the alternate screen: uncounted, Handoff prints them.
func (m *Dashboard) apply(ev dispatch.Event) (quit bool) {
	if m.quitting {
		return false
	}
	m.received++
	if re, ok := rowEvents[ev.Kind]; ok {
		if ev.Kind == dispatch.EvClosed {
			m.closed++
		}
		m.setRow(ev.Ticket, re.state, re.note(ev.Detail))
		return false
	}
	switch ev.Kind {
	case dispatch.EvDispatch:
		m.queue = ev.QueueState
		m.pickUp(ev.Ticket, ev.Title)
	case dispatch.EvQueue:
		m.queue = ev.QueueState
	case dispatch.EvShared: // another run on the machine started or ended
		m.shared = ev.Shared
	case dispatch.EvWarn:
		// Only a ticket the warning sets aside is for review, its row saying why as a deferred
		// one does, or blocked, when only its merge needs the maintainer (MERGE_CONFLICT). The
		// rest are about a ticket still running (LONG_RUNNING) or already deferred
		// (TRIAGE_FAILED), whose row stays as it is.
		if ev.Ticket != "" && ev.Aside && !m.markBlocked(ev) {
			why := ev.Detail
			if why == "" {
				why = "left for review, see the log"
			}
			m.setRow(ev.Ticket, rowReview, why)
		}
	case dispatch.EvAnswered: // its title for a ticket carried over from the last run, with no row yet
		m.pickUp(ev.Ticket, ev.Title)
	case dispatch.EvTriage:
		m.triaged++
		// A verdict on an earlier deferral, in after the ticket came back, would outlast the next.
		if i := m.rowIndex(ev.Ticket); i >= 0 && m.rows[i].state == rowDeferred {
			m.rows[i].triage = ev.Detail + " · " + ev.Title
		}
	case dispatch.EvDrain, dispatch.EvResume: // as asked here, or with SIGUSR1
		m.setDraining(ev.Kind == dispatch.EvDrain)
	case dispatch.EvProbed: // the machine works again after an environment hold
		m.stopping = false
	case dispatch.EvLimited: // no new tickets until the usage limit resets
		m.limited, m.limitReset = true, ev.Reset
	case dispatch.EvLimitLift:
		m.limited, m.limitReset = false, time.Time{}
	case dispatch.EvHold:
		m.stopping = true
		// A stop found before dispatching belongs to no ticket; one met as a finished ticket
		// merged (DIRTY_TREE, say) blocks it.
		if ev.Ticket != "" && !m.markBlocked(ev) {
			m.setRow(ev.Ticket, rowStopped, "")
		}
	case dispatch.EvStop, dispatch.EvDone:
		if ev.Kind == dispatch.EvStop {
			m.markBlocked(ev) // the ticket the run stopped over, as it merged, with nothing else running
			// Each to the end as it stops, in the order they were.
			for _, r := range slices.Clone(m.rows) {
				if rowLooks[r.state].live {
					m.setRow(r.id, rowStopped, r.note)
				}
			}
		}
		// main prints the last line after the program exits, below the run's summary.
		m.final, m.queue.Solo, m.quitting = &ev, dispatch.SoloState{}, true
		return true
	}
	return false
}
