package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A ticket that started before a ticket blocking it merged waits, back in the queue: its row says
// for what, and goes back to working in place once it is dispatched again. It counts as neither
// deferred nor needing the maintainer.
func TestWaitingTicketShowsWhatItWaitsForAndComesBack(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Add the parser"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "k-2", Title: "Use the parser"},
		dispatch.Event{Kind: dispatch.EvWaits, Ticket: "k-2", Title: "Use the parser", Detail: "k-1"})
	if len(m.rows()) != 2 || m.rows()[1].state != rowWaiting || m.setAside() != 0 || m.needsYou() != 0 {
		t.Fatalf("rows %+v, set aside %d, needs you %d; want k-2 waiting", m.rows(), m.setAside(), m.needsYou())
	}
	m.width, m.height = 80, 40
	if v := ansi.Strip(m.View()); !strings.Contains(v, "◷ waiting") || !strings.Contains(v, "for k-1 to merge") {
		t.Errorf("view lacks k-2 waiting for k-1:\n%s", v)
	}
	got := ansi.Strip(renderEvent(dispatch.Event{Kind: dispatch.EvWaits, Ticket: "k-2", Detail: "k-1"}))
	if !strings.Contains(got, "◷ k-2 waits  for k-1 to merge") {
		t.Errorf("rendered %q", got)
	}

	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 3, Ticket: "k-2", Title: "Use the parser"})
	if len(m.rows()) != 2 || m.rows()[1].state != rowWorking || m.rows()[1].note != "" {
		t.Errorf("rows %+v; want k-2's row working again", m.rows())
	}
}
