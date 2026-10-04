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

// printedEnd is what main prints once the dashboard m has closed, on a terminal width columns wide:
// the run's summary, then the run's last line.
func printedEnd(m Dashboard, width int) string {
	var b strings.Builder
	p := Printer{Out: &b, Styled: true, Width: width}
	p.Summary(m)
	p.End(m)
	return b.String()
}

// TestSummaryAfterTheDashboardIsWhole: the alternate screen takes the dashboard with it, so main
// prints the run's summary on the normal screen, where it isn't cut to the pane's height: the title,
// the totals and every ticket, as wide as the terminal is now, then the run's last line.
func TestSummaryAfterTheDashboardIsWhole(t *testing.T) {
	m := endedRun(40)
	m.width, m.height = 120, 8 // the pane as the dashboard last saw it: short, and wider than now
	out := ansi.Strip(printedEnd(m, 80))
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if top := lines[min(1, len(lines)-1)]; !strings.Contains(top, "Orchestra") || // below a margin
		!strings.Contains(top, "batch/2026-10-02") {
		t.Errorf("the summary should start with the title:\n%s", out)
	}
	if !strings.Contains(out, "✓ 40") {
		t.Errorf("the summary lacks the totals:\n%s", out)
	}
	for i := range 40 {
		if id := fmt.Sprintf("kinieta-%03d", i); !strings.Contains(out, id+" ") {
			t.Errorf("the summary lacks %s:\n%s", id, out)
		}
	}
	if strings.Contains(out, "earlier tickets") {
		t.Errorf("the summary left tickets out:\n%s", out)
	}
	if last := lines[len(lines)-1]; !strings.HasPrefix(last, "♪ Completed the Run  no tickets") {
		t.Errorf("the run's last line should follow the summary, not %q", last)
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > 80 {
			t.Errorf("line %d wide on a terminal 80 wide: %q", ansi.StringWidth(l), l)
		}
	}
}

// A run that picked up no ticket has no tickets table, and no blank line in its place; a dashboard
// that never started has no summary.
func TestSummaryWithoutTickets(t *testing.T) {
	out := ansi.Strip(printedEnd(endedRun(0), 80))
	if strings.Contains(out, "Tickets") || strings.Contains(out, "\n\n") {
		t.Errorf("a run without tickets printed:\n%s", out)
	}
	if !strings.Contains(out, "Orchestra") || !strings.Contains(out, "♪ Completed the Run") {
		t.Errorf("the summary or the last line is missing:\n%s", out)
	}
	var b strings.Builder
	Printer{Out: &b, Styled: true, Width: 80}.Summary(Dashboard{})
	if b.Len() != 0 {
		t.Errorf("a dashboard that never started printed %q", b.String())
	}
}
