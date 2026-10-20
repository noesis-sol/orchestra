package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A worker files tickets itself and Beads checks only an ID's prefix, so a ticket's ID can carry
// escape sequences: the run sets such a ticket aside, and one with a C1 control character even gets
// a worker. The dashboard shows the ID without them in the tickets table, the run's summary after
// it, the worker list and box, the solo line and the stop question, and keeps its own colours.
func TestDashboardDropsControlSequencesInTicketIDs(t *testing.T) {
	styled(t)
	for name, id := range attacks {
		m := runEvents(reviewDashboard(t),
			dispatch.Event{Kind: dispatch.EvDeferred, Ticket: id, Detail: "its ID can't name a branch"},
			dispatch.Event{Kind: dispatch.EvWarn, Ticket: id + "2", Aside: true, Text: "WARN not deferred"},
			dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: id + "3", Title: "Fix it",
				QueueState: dispatch.QueueState{Solo: dispatch.SoloState{Ticket: id + "3"}}})
		next, _ := m.Update(statusMsg(dispatch.Status{Ticket: id + "3", Title: "Fix it", Started: time.Now(),
			Agent: dispatch.StateWorking, Activity: "Fix it"}))
		m = next.(Dashboard)
		view := m.View()
		checkPrintable(t, name+" in the view", view)
		if !sgr.MatchString(view) {
			t.Errorf("%s: the view lost its colours:\n%q", name, view)
		}
		checkPrintable(t, name+" in the tickets table", m.ticketsTable(m.width, 100))
		checkPrintable(t, name+" in the worker list", m.workerList(m.width))
		checkPrintable(t, name+" in the worker's box", m.workerPanels(m.width))
		checkPrintable(t, name+" in the title line", m.titleLine(200))
		checkPrintable(t, name+" in the stop question", m.drainQuestion().about)
		var b strings.Builder
		Printer{Out: &b, Width: 120}.Summary(m)
		checkPrintable(t, name+" in the summary", b.String())
	}
}
