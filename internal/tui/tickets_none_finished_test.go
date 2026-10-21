package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// noneFinishedDashboard is the run of the report: an 85-column pane, three workers, its only ticket
// merging, none completed and the rest of the queue held back.
func noneFinishedDashboard(t *testing.T) Dashboard {
	return run(t).config(dispatch.Config{Limit: 40, Base: "main", Concurrency: 3, MCP: noServers}).
		picked("demo-477", "Add the parser").
		running(dispatch.Status{Ticket: "demo-477", Title: "Add the parser", Started: time.Now().Add(-40 * time.Second),
			Agent: dispatch.StateDone, Merge: dispatch.MergeRunning}).
		sized(85, 40).dash()
}

// With no ticket come to an end yet, the tickets table beside Current has no row of its own: its
// first line says where the running tickets are and when they come in, rather than an empty frame.
func TestTicketsTableSaysWhyItIsEmpty(t *testing.T) {
	m := noneFinishedDashboard(t)
	table := tableInView(t, m)
	if strings.Contains(table, "demo-477") {
		t.Errorf("the tickets table repeats demo-477, which Current shows:\n%s", table)
	}
	lines := strings.Split(strings.TrimSpace(table), "\n")
	if len(lines) < 3 || !strings.Contains(lines[1], emptyTableNote) {
		t.Errorf("the empty tickets table's first row should say %q:\n%s", emptyTableNote, table)
	}
	if view := ansi.Strip(m.View()); !strings.Contains(view, "demo-477") || !strings.Contains(view, "merging") {
		t.Errorf("Current should show demo-477 merging:\n%s", view)
	}
}

// The note gives way to the first row that comes in, and the table keeps its height.
func TestTicketsTableNoteGivesWayToARow(t *testing.T) {
	m := noneFinishedDashboard(t)
	before := strings.Count(tableInView(t, m), "\n")
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvClosed, Ticket: "demo-477", Detail: "abc1234 merged into main"})
	m = setStatus(m, dispatch.Status{Ticket: "demo-477", Gone: true})
	view := ansi.Strip(m.View())
	_, table, _ := strings.Cut(view, "  Tickets")
	table, _, _ = strings.Cut(table, "  Current")
	if strings.Contains(table, emptyTableNote) || !strings.Contains(table, "demo-477") {
		t.Errorf("the merged ticket's row should replace the note:\n%s", table)
	}
	if after := strings.Count(table, "\n"); after != before {
		t.Errorf("the table went from %d lines to %d", before, after)
	}
}

// A pane too narrow for the whole note cuts it rather than wrap it.
func TestTicketsTableNoteFitsANarrowPane(t *testing.T) {
	m := noneFinishedDashboard(t)
	for _, w := range []int{minFrameWidth, 40, 85, 160} {
		table := strings.Split(ansi.Strip(m.rowsTable(w, 5, nil, emptyTableNote)), "\n")
		if len(table) != 5 {
			t.Errorf("at %d columns the table takes %d lines, want 5:\n%s", w, len(table), strings.Join(table, "\n"))
		}
	}
}
