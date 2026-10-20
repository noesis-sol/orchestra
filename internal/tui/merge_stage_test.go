package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// mergeDashboard is a dashboard with three tickets picked up: one merging, one second in line to
// merge, and one whose worker still works.
func mergeDashboard(t *testing.T) Dashboard {
	t.Helper()
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch", Concurrency: 3}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "kinieta-m1", Title: "Merging now"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "kinieta-q2", Title: "Second in line"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 3, Ticket: "kinieta-w3", Title: "Still at work"})
	m.width, m.height = 90, 40
	now := time.Now()
	for _, st := range []dispatch.Status{
		{Ticket: "kinieta-m1", Title: "Merging now", Started: now.Add(-3 * time.Minute), Agent: dispatch.StateDone,
			Merge: dispatch.MergeRunning, Activity: "merging into batch"},
		{Ticket: "kinieta-q2", Title: "Second in line", Started: now.Add(-2 * time.Minute), Agent: dispatch.StateDone,
			Merge: dispatch.MergeQueued, Place: 2},
		{Ticket: "kinieta-w3", Title: "Still at work", Started: now.Add(-time.Minute), Agent: dispatch.StateWorking},
	} {
		m = withStatus(m, statusMsg(st))
	}
	return m
}

func withStatus(m Dashboard, msg statusMsg) Dashboard {
	next, _ := m.Update(msg)
	return next.(Dashboard)
}

func TestFinishedTicketsShowTheirMergeStage(t *testing.T) {
	m := mergeDashboard(t)
	view := fullTable(m) + "\n" + ansi.Strip(m.View()) // the table's rows where Current isn't drawn, then Current
	for _, want := range []string{
		"◆ merging", "◆  kinieta-m1  merging ", // the table's row, then Current's head
		"◼ to merge", "◼  kinieta-q2  to merge (2nd) ",
		"▶ working", "kinieta-w3  working ",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("the view lacks %q:\n%s", want, view)
		}
	}
	for line := range strings.SplitSeq(view, "\n") {
		if (strings.Contains(line, "kinieta-m1") || strings.Contains(line, "kinieta-q2")) &&
			(strings.Contains(line, "done") || strings.Contains(line, "working")) {
			t.Errorf("a finished ticket's line should say neither done nor working: %q", line)
		}
	}
}

func TestFinishedWorkerHasNoSpinner(t *testing.T) {
	m := mergeDashboard(t)
	spinning := m.spin.View()
	for _, c := range []struct {
		st     dispatch.Status
		static string // "": the spinner
	}{
		{dispatch.Status{Agent: dispatch.StateWorking}, ""},
		{dispatch.Status{Agent: dispatch.StateIdle, Resolving: true}, ""},
		{dispatch.Status{Agent: dispatch.StateWorking, Fixing: true}, ""},
		{dispatch.Status{Agent: dispatch.StateDone}, "• "},
		{dispatch.Status{Agent: dispatch.StateDone, Merge: dispatch.MergeQueued, Place: 1}, "◼ "},
		{dispatch.Status{Agent: dispatch.StateDone, Merge: dispatch.MergeRunning}, "◆ "},
	} {
		got := m.mark(c.st)
		if c.static == "" {
			if got != spinning {
				t.Errorf("a worker at work (%+v) should have the spinner, not %q", c.st, got)
			}
			continue
		}
		if ansi.Strip(got) != c.static || ansi.StringWidth(got) != ansi.StringWidth(spinning) {
			t.Errorf("a finished worker (%+v) should have the static mark %q as wide as the spinner, not %q",
				c.st, c.static, got)
		}
	}
}

func TestHandedBackTicketJoinsTheMergeQueueAgain(t *testing.T) {
	m := mergeDashboard(t)
	st := m.active["kinieta-m1"]
	for _, c := range []struct {
		name       string
		set        func(*dispatch.Status)
		row, label string
	}{
		{"handed back to fix its check", func(s *dispatch.Status) {
			s.Merge, s.Fixing, s.Agent = dispatch.MergeNone, true, dispatch.StateWorking
		}, "⟳ fixing", "kinieta-m1  fixing check "},
		{"back in line once fixed", func(s *dispatch.Status) {
			s.Merge, s.Place, s.Fixing, s.Agent = dispatch.MergeQueued, 1, false, dispatch.StateDone
		}, "◼ to merge", "kinieta-m1  to merge (1st) "},
		{"handed back to resolve conflicts", func(s *dispatch.Status) {
			s.Merge, s.Place, s.Resolving, s.Agent = dispatch.MergeNone, 0, true, dispatch.StateIdle
		}, "⟳ resolving", "kinieta-m1  resolving "},
		{"merging once resolved", func(s *dispatch.Status) {
			s.Merge, s.Resolving, s.Agent = dispatch.MergeRunning, false, dispatch.StateDone
		}, "◆ merging", "kinieta-m1  merging "},
	} {
		c.set(&st)
		m = withStatus(m, statusMsg(st))
		if view := ansi.Strip(m.View()); !strings.Contains(fullTable(m), c.row) || !strings.Contains(view, c.label) {
			t.Errorf("%s: the view should show %q in the table and %q in Current:\n%s", c.name, c.row, c.label, view)
		}
	}
}

func TestMergeStagesHaveColoursOfTheirOwn(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	others := []lipgloss.Style{pickedStyle, closedStyle, deferredStyle, stopStyle, doneStyle, organStyle, sayStyle,
		testingStyle, dimStyle, ticketsStyle, keyStyle}
	for _, s := range []lipgloss.Style{toMergeStyle, mergingStyle} {
		for _, o := range others {
			if o.GetForeground() == s.GetForeground() {
				t.Errorf("a merge stage's colour %v already means another state", s.GetForeground())
			}
		}
	}
	if toMergeStyle.GetForeground() == mergingStyle.GetForeground() {
		t.Error("to merge and merging should have colours of their own")
	}
	m := mergeDashboard(t)
	if v := m.ticketsTable(m.width, 12); !strings.Contains(v, toMergeStyle.Render("◼ to merge")) ||
		!strings.Contains(v, mergingStyle.Render("◆ merging")) {
		t.Errorf("the table should show the merge stages in their colours:\n%q", v)
	}
}

func TestOrdinal(t *testing.T) {
	for n, want := range map[int]string{1: "1st", 2: "2nd", 3: "3rd", 4: "4th", 11: "11th", 12: "12th", 13: "13th",
		21: "21st", 22: "22nd", 101: "101st", 111: "111th"} {
		if got := ordinal(n); got != want {
			t.Errorf("ordinal(%d) = %q, want %q", n, got, want)
		}
	}
}
