package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// fullTable is m's tickets table with every row, as it is drawn where Current isn't, unstyled.
func fullTable(m Dashboard) string { return ansi.Strip(m.ticketsTable(m.width, 12)) }

// tableInView is the tickets table of m's view, its Tickets label to the Current label, unstyled.
func tableInView(t *testing.T, m Dashboard) string {
	t.Helper()
	view := ansi.Strip(m.View())
	_, rest, ok := strings.Cut(view, "  Tickets")
	table, _, current := strings.Cut(rest, "  Current")
	if !ok || !current {
		t.Fatalf("the view should have the tickets table above Current:\n%s", view)
	}
	return table
}

// besideCurrentDashboard has two tickets that came to an end, k-1 merged and k-2 deferred, and two
// whose workers are at them, k-3 and k-4.
func besideCurrentDashboard(t *testing.T) Dashboard {
	now := time.Now()
	return run(t).config(dispatch.Config{Limit: 40, Base: "batch", Concurrency: 2}).
		picked("k-1", "Merged first").picked("k-2", "Deferred second").
		closed("k-1", "abc1234 merged into batch").
		event(dispatch.Event{Kind: dispatch.EvDeferred, Ticket: "k-2", Detail: "still open"}).
		running(dispatch.Status{Ticket: "k-3", Title: "Still at work", Started: now.Add(-2 * time.Minute),
			Agent: dispatch.StateWorking},
			dispatch.Status{Ticket: "k-4", Title: "Running its checks", Started: now.Add(-time.Minute),
				Agent: dispatch.StateWorking, Doing: dispatch.DoingTesting}).
		sized(90, 40).dash()
}

// With Current drawn, the tickets table leaves out the tickets it shows: its rows are those that came
// to an end.
func TestTicketsTableLeavesOutWhatCurrentShows(t *testing.T) {
	m := besideCurrentDashboard(t)
	table := tableInView(t, m)
	for _, id := range []string{"k-3", "k-4"} {
		if strings.Contains(table, id) {
			t.Errorf("the tickets table repeats %s, which Current shows:\n%s", id, table)
		}
	}
	if !strings.Contains(table, "✓ done") || !strings.Contains(table, "k-1") ||
		!strings.Contains(table, "↷ deferred") || !strings.Contains(table, "k-2") {
		t.Errorf("the tickets table should list the tickets that came to an end:\n%s", table)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "k-3  working") || !strings.Contains(view, "k-4  testing") {
		t.Errorf("Current should show both running tickets:\n%s", view)
	}
}

// Where Current isn't drawn, the running tickets keep their rows: the run's summary, and the table on
// its own.
func TestTicketsTableKeepsRunningTicketsWithoutCurrent(t *testing.T) {
	m := besideCurrentDashboard(t)
	for name, table := range map[string]string{"the summary": ansi.Strip(m.summary(90)), "the table": fullTable(m)} {
		if !strings.Contains(table, "▶ working") || !strings.Contains(table, "k-3") ||
			!strings.Contains(table, "▶ testing") || !strings.Contains(table, "k-4") {
			t.Errorf("%s should have the running tickets' rows:\n%s", name, table)
		}
	}
}

// A ticket comes into the table as it leaves Current, under those that came to an end before it,
// whenever it was picked up.
func TestFinishedTicketJoinsTheTableAsItLeavesCurrent(t *testing.T) {
	m := besideCurrentDashboard(t)
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvClosed, Ticket: "k-4", Detail: "def5678 merged into batch"})
	m = setStatus(m, dispatch.Status{Ticket: "k-4", Gone: true})
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvAsked, Ticket: "k-3", Detail: "which way?"})
	m = setStatus(m, dispatch.Status{Ticket: "k-3", Gone: true})
	view := ansi.Strip(m.View())
	_, rest, _ := strings.Cut(view, "  Tickets")
	order := []string{"k-1", "k-2", "k-4", "k-3"}
	at := 0
	for _, id := range order {
		i := strings.Index(rest[at:], id)
		if i < 0 {
			t.Fatalf("the tickets table should list %v in the order they came to an end:\n%s", order, view)
		}
		at += i + len(id)
	}
}

// The count of earlier tickets and the blank rows count only the rows the table shows.
func TestTicketsTableBesideCurrentCountsTheRowsItShows(t *testing.T) {
	m := besideCurrentDashboard(t)
	rows := m.rowsBesideCurrent()
	if len(rows) != 2 {
		t.Fatalf("rows beside Current: %v, want k-1 and k-2", rows)
	}
	short := ansi.Strip(m.rowsTable(90, 3, rows, emptyTableNote)) // one row: the count, k-2 giving way to it
	if !strings.Contains(short, "+2 ") || !strings.Contains(short, "earlier tickets") {
		t.Errorf("the count should be of the rows the table shows, 2:\n%s", short)
	}
	table := strings.Split(ansi.Strip(m.rowsTable(90, 6, rows, emptyTableNote)), "\n")
	if len(table) != 6 || strings.TrimSpace(strings.ReplaceAll(table[3], "│", "")) != "" ||
		strings.TrimSpace(strings.ReplaceAll(table[4], "│", "")) != "" {
		t.Errorf("blank rows should fill what the two rows leave:\n%s", strings.Join(table, "\n"))
	}
}
