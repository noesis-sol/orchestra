package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The In queue box gives the ready tickets waiting, then, dim, how many aren't closed: "1 of 5", as
// the Workers box beside it does. The one-line totals say the same, and winding down still marks
// the queue held after it.
func TestInQueueShowsTheTicketsNotClosed(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
	m := NewDashboard(dispatch.Config{Limit: 40, Concurrency: 4}, func() {}, func(bool) {}, func(string) {})
	m.width, m.height = 120, 40
	if strip := ansi.Strip(m.statsTable(120)); strings.Contains(strip, " of 5") || !strings.Contains(strip, "—") {
		t.Errorf("before the loop says, the queue is —:\n%s", strip)
	}
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "First",
		QueueState: dispatch.QueueState{Queued: 2, Unclosed: 6}},
		dispatch.Event{Kind: dispatch.EvQueue, QueueState: dispatch.QueueState{Queued: 1, Unclosed: 5}})
	table := m.statsTable(120)
	if of := dimStyle.Render(" of 5"); of == " of 5" || !strings.Contains(table, "1"+of) {
		t.Errorf("the In queue box doesn't read 1 and a dim \" of 5\": %q", table)
	}
	if strip := ansi.Strip(table); !strings.Contains(strip, "1 of 5") {
		t.Errorf("strip:\n%s", strip)
	}
	if line := ansi.Strip(m.statsLine(120)); !strings.Contains(line, "queue 1 of 5") {
		t.Errorf("one-line totals: %s", line)
	}

	m.draining = true
	if strip := ansi.Strip(m.statsTable(120)); !strings.Contains(strip, "1 of 5 · held") {
		t.Errorf("winding down:\n%s", strip)
	}
	if line := ansi.Strip(m.statsLine(120)); !strings.Contains(line, "queue 1 of 5 held") {
		t.Errorf("one-line totals winding down: %s", line)
	}

	// Too narrow for the strip, the one-line totals take over, the count with them.
	if narrow := ansi.Strip(m.statsTable(50)); strings.Contains(narrow, "In queue") || !strings.Contains(narrow, "queue 1 of 5") {
		t.Errorf("at 50 columns:\n%s", narrow)
	}
}
