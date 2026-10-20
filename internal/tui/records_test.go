package tui

import (
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A ticket has one record, whatever comes of it first: a worker's status alone shows it nowhere until
// an event gives it a row, which comes last, and its Gone status leaves nothing of it without one.
func TestEachTicketHasOneRecord(t *testing.T) {
	m := run(t).picked("k-1", "One").dash()
	m = receive(m, statusMsg(dispatch.Status{Ticket: "k-2"}), statusMsg(dispatch.Status{Ticket: "k-3"}))
	if got := m.rows(); len(got) != 1 || got[0].id != "k-1" {
		t.Fatalf("rows %+v, want k-1's alone: a status gives no row", got)
	}
	if ids := m.runningIDs(); !slices.Equal(ids, []string{"k-2", "k-3"}) {
		t.Errorf("running %q, want k-2 and k-3", ids)
	}
	m = receive(m, statusMsg(dispatch.Status{Ticket: "k-3", Gone: true}))
	if len(m.records) != 2 {
		t.Errorf("records %+v: k-3, gone with no row, should have none", m.records)
	}
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvAnswered, Ticket: "k-2", Title: "Two"},
		dispatch.Event{Kind: dispatch.EvDispatch, Ticket: "k-1", Title: "One again"})
	var ids []string
	for _, r := range m.rows() {
		ids = append(ids, r.id+" "+r.title)
	}
	if want := []string{"k-1 One again", "k-2 Two"}; !slices.Equal(ids, want) {
		t.Errorf("rows %q, want %q: one each, k-2's last", ids, want)
	}
	if beside := m.rowsBesideCurrent(); len(beside) != 1 || beside[0].row.id != "k-1" {
		t.Errorf("beside Current: %+v, want k-1's row alone, k-2 being in Current", beside)
	}
	m = receive(m, statusMsg(dispatch.Status{Ticket: "k-2", Gone: true}))
	if beside := m.rowsBesideCurrent(); len(beside) != 2 || len(m.Running()) != 0 {
		t.Errorf("beside Current: %+v, running %v; want both rows, nothing running", beside, m.Running())
	}
}
