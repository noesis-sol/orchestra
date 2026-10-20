package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// An empty queue reads "0 of 4" all in the faint grey, as the strip's other zeros do, so it doesn't
// draw the eye; a bare 0 before the tickets not closed are known is faint too, and a queue with
// tickets in it keeps its count bright.
func TestEmptyQueueIsFaint(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
	m := NewDashboard(dispatch.Config{Limit: 40, Concurrency: 4}, func() {}, func(bool) {}, func(string) {})
	m.width, m.height = 120, 40

	m.queue.Queued, m.queue.Unclosed = 0, -1
	if table := m.statsTable(120); !strings.Contains(table, dimStyle.Render("0")) {
		t.Errorf("a bare empty queue isn't faint: %q", table)
	}

	m = runEvents(m, dispatch.Event{Kind: dispatch.EvQueue, QueueState: dispatch.QueueState{Queued: 0, Unclosed: 4}})
	table := m.statsTable(120)
	if faint := dimStyle.Render("0 of 4"); faint == "0 of 4" || !strings.Contains(table, faint) {
		t.Errorf("the In queue box doesn't read a faint \"0 of 4\": %q", table)
	}
	if strip := ansi.Strip(table); !strings.Contains(strip, "0 of 4") {
		t.Errorf("strip:\n%s", strip)
	}

	m = runEvents(m, dispatch.Event{Kind: dispatch.EvQueue, QueueState: dispatch.QueueState{Queued: 2, Unclosed: 4}})
	table = m.statsTable(120)
	if !strings.Contains(table, "2"+dimStyle.Render(" of 4")) || strings.Contains(table, dimStyle.Render("2 of 4")) {
		t.Errorf("a queue of 2 isn't a bright 2 and a faint \" of 4\": %q", table)
	}
}
