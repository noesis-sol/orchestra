package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// checkWithinPane fails t for a view taller or wider than the pane.
func checkWithinPane(t *testing.T, view string, w, h int) {
	t.Helper()
	lines := strings.Split(view, "\n")
	if len(lines) > h {
		t.Errorf("%dx%d: view is %d lines:\n%s", w, h, len(lines), ansi.Strip(view))
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > w {
			t.Errorf("%dx%d: line %d wide: %q", w, h, ansi.StringWidth(l), ansi.Strip(l))
		}
	}
}

// A title, a triage verdict or a question with line breaks in it takes one line of its row and of
// the worker list, so the tickets table still fits beside the worker's box.
func TestTextWithLineBreaksTakesOneLine(t *testing.T) {
	m := runEvents(reviewDashboard(),
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Fix the\nthing"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "k-2", Title: "Second"},
		dispatch.Event{Kind: dispatch.EvDeferred, Ticket: "k-2", Detail: "still open"},
		dispatch.Event{Kind: dispatch.EvTriage, Ticket: "k-2", Detail: "retry", Title: "The worker\r\nran out\tof time\n"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 3, Ticket: "k-3", Title: "Third"},
		dispatch.Event{Kind: dispatch.EvAsked, Ticket: "k-3", Detail: "q-9 Which\nway?"})
	next, _ := m.Update(statusMsg(dispatch.Status{Ticket: "k-1", Title: "Fix the\nthing", Started: time.Now(),
		Agent: dispatch.StateWorking, Activity: "Bash(go test\n./...)"}))
	m = next.(Dashboard)
	m.height = 22

	if table := m.ticketsTable(m.width, 100); lipgloss.Height(table) != len(m.rows)+4 {
		t.Errorf("the tickets table should take a line per row:\n%s", ansi.Strip(table))
	}
	if list := m.workerList(m.width); lipgloss.Height(list) != 3 || !strings.Contains(ansi.Strip(list), "Fix the thing") {
		t.Errorf("the worker list should take a line per worker:\n%s", ansi.Strip(list))
	}
	view := m.View()
	checkWithinPane(t, view, m.width, m.height)
	v := ansi.Strip(view)
	for _, want := range []string{"Completed", "Tickets", "Fix the thing", "◆ retry · The worker ran out of time",
		"answer q-9 Which way?", "Bash(go test ./...)"} {
		if !strings.Contains(v, want) {
			t.Errorf("the view should show %q on one line:\n%s", want, v)
		}
	}
}

// A wide character the question's box cuts in half at either edge is blanked, not kept whole.
func TestOverlayBlanksAWideCharacterTheBoxCuts(t *testing.T) {
	if got := ansi.Strip(overlay("ab日本cd", "XY", 8)); got != "ab XY cd" {
		t.Errorf("overlay = %q, want %q", got, "ab XY cd")
	}
	if got := ansi.Strip(overlay("abcd日本", "XY", 8)); got != "abcXY 本" {
		t.Errorf("overlay = %q, want %q", got, "abcXY 本")
	}
}

// With wide characters under the question's box, the view stays within the pane at any size.
func TestDrainQuestionOverWideCharactersFitsThePane(t *testing.T) {
	var calls []bool
	cancelled := false
	base := drainDashboard(&calls, &cancelled)
	base.active["k-1"] = dispatch.Status{Ticket: "k-1", Title: strings.Repeat("日本語", 20),
		Started: time.Now().Add(-2 * time.Minute), Agent: "working"}
	base.active["k-2"] = dispatch.Status{Ticket: "k-2", Title: "x" + strings.Repeat("チケット🙂", 15),
		Started: time.Now().Add(-time.Minute), Agent: "working"}
	base = runEvents(base,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: strings.Repeat("日本語", 20)},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "k-2", Title: "x" + strings.Repeat("チケット🙂", 15)},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 3, Ticket: "k-30", Title: "ab" + strings.Repeat("表示", 30)})
	m := press(base, "s")
	for w := 36; w <= 130; w++ {
		for _, h := range []int{16, 24, 40} {
			m.width, m.height = w, h
			checkWithinPane(t, m.View(), w, h)
		}
	}
}
