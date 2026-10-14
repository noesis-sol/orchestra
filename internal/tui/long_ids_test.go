package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Long ticket IDs are cut to a third of the pane, so the titles and notes beside them keep a readable
// width; IDs are measured in cells, not bytes.
func TestLongTicketIDsLeaveRoomForTitles(t *testing.T) {
	long := "project-" + strings.Repeat("x", 32)       // 40 characters
	wide := "проект-" + strings.Repeat("я", 12) + ".1" // 21 cells, 40 bytes
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: long, Title: "Keep titles visible"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: wide, Title: "Measure in cells"},
		dispatch.Event{Kind: dispatch.EvDeferred, Ticket: wide, Detail: "still open, noted for review"})
	table := ansi.Strip(m.ticketsTable(60, 6))
	for line := range strings.SplitSeq(table, "\n") {
		if w := ansi.StringWidth(line); w > 60 {
			t.Errorf("line %d cells wide, over the pane's 60: %q", w, line)
		}
	}
	for _, want := range []string{"project-xxxxxxxxxxx…", "Keep titles visible", "проект-яяяяяяяяяяяя…", "still open, noted"} {
		if !strings.Contains(table, want) {
			t.Errorf("table lacks %q:\n%s", want, table)
		}
	}
	if strings.Contains(table, long) {
		t.Errorf("a 40-character ID should be cut at width 60:\n%s", table)
	}
}
