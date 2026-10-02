package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// endedRun is a dashboard whose run picked up and closed n tickets, then ended.
func endedRun(n int) Dashboard {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch/2026-10-02"}, func() {}, func(bool) {}, func(string) {})
	for i := range n {
		id := fmt.Sprintf("kinieta-%03d", i)
		m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: i + 1, Ticket: id, Title: "A ticket"},
			dispatch.Event{Kind: dispatch.EvClosed, Ticket: id, Detail: "abc1234 merged into batch/2026-10-02"})
	}
	return runEvents(m, dispatch.Event{Kind: dispatch.EvDone, Text: "READY_EMPTY"})
}

// TestSummaryKeepsItsTopInAShortPane: Bubble Tea never writes the top lines of a frame taller than the
// pane, so the summary must fit, newest tickets and all, below its title and totals.
func TestSummaryKeepsItsTopInAShortPane(t *testing.T) {
	m := endedRun(40)
	if !m.quitting {
		t.Fatal("the run's end should quit the dashboard")
	}
	for _, size := range [][2]int{{80, 30}, {40, 30}, {120, 12}, {80, 8}, {80, 5}, {30, 4}} {
		m.width, m.height = size[0], size[1]
		view := m.View()
		lines := strings.Split(view, "\n") // as Bubble Tea counts them: the last one is the cursor's
		if len(lines) > m.height {
			t.Errorf("%dx%d: the summary is %d lines", size[0], size[1], len(lines))
		}
		if !strings.HasSuffix(view, "\n") {
			t.Errorf("%dx%d: the summary should end with a newline for main's last line", size[0], size[1])
		}
		if top := ansi.Strip(strings.Join(lines[:2], "\n")); !strings.Contains(top, "Orchestra") { // below a margin
			t.Errorf("%dx%d: the summary should start with the title, not %q", size[0], size[1], top)
		}
		if !strings.Contains(ansi.Strip(view), "✓ 40") {
			t.Errorf("%dx%d: the summary lacks the totals:\n%s", size[0], size[1], ansi.Strip(view))
		}
		for _, l := range lines {
			if ansi.StringWidth(l) > m.width {
				t.Errorf("%dx%d: line %d wide: %q", size[0], size[1], ansi.StringWidth(l), ansi.Strip(l))
			}
		}
		if size == [2]int{80, 30} {
			v := ansi.Strip(view)
			if !strings.Contains(v, "earlier tickets, see the log") || !strings.Contains(v, "kinieta-039") {
				t.Errorf("80x30: the table should end with the newest ticket and count the earlier ones:\n%s", v)
			}
			t.Logf("summary at 80x30:\n%s", v)
		}
	}
}

// TestSummaryShowsEveryTicketWhereItFits: an unsized dashboard, or a pane tall enough, keeps every row.
func TestSummaryShowsEveryTicketWhereItFits(t *testing.T) {
	m := endedRun(40)
	for _, height := range []int{0, 60} {
		m.width, m.height = 80, height
		v := ansi.Strip(m.View())
		if !strings.Contains(v, "kinieta-000") || strings.Contains(v, "earlier tickets") {
			t.Errorf("height %d: every ticket should be listed:\n%s", height, v)
		}
	}
	m = endedRun(2)
	m.width, m.height = 80, 13 // the title 2 lines, the totals 4, a table of two rows 6, the cursor 1
	if v := ansi.Strip(m.View()); !strings.Contains(v, "kinieta-000") || !strings.Contains(v, "kinieta-001") {
		t.Errorf("a short table that fits should be shown whole:\n%s", v)
	}
}
