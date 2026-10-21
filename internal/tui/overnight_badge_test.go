package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// An overnight run's title line carries the blue overnight badge after the version, for the whole run;
// another run's carries none.
func TestTitleLineShowsTheOvernightBadge(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	m := NewDashboard(dispatch.Config{Version: "0.9.0", Base: "main", Concurrency: 2, Overnight: true},
		func() {}, func(bool) {}, func(string) {})
	m.width, m.height = 120, 30
	badge := overnightStyle.Render("overnight")
	if line := m.titleLine(m.width); !strings.Contains(line, badge) {
		t.Errorf("title line %q lacks the badge %q", line, badge)
	}
	if v := ansi.Strip(m.titleLine(m.width)); !strings.Contains(v, "0.9.0  overnight") || !strings.Contains(v, "main") {
		t.Errorf("title line %q, want the badge after the version and the branch", v)
	}
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "A ticket"})
	m.stopping = true
	if v := ansi.Strip(m.titleLine(m.width)); !strings.Contains(v, "overnight") || !strings.Contains(v, "stopping") {
		t.Errorf("title line %q, want the badge as the run stops", v)
	}

	other := NewDashboard(dispatch.Config{Version: "0.9.0", Base: "main", Concurrency: 2},
		func() {}, func(bool) {}, func(string) {})
	if v := ansi.Strip(other.titleLine(120)); strings.Contains(v, "overnight") {
		t.Errorf("title line %q of a run that isn't overnight", v)
	}
}

// In a pane too narrow for the whole title line the badge gives way first: the line is the one without
// it, cut at the pane's width, and never wider than the pane.
func TestTitleLineOvernightBadgeGivesWayFirst(t *testing.T) {
	m := NewDashboard(dispatch.Config{Version: "0.9.0", Base: "main", Concurrency: 2, Overnight: true},
		func() {}, func(bool) {}, func(string) {})
	full := ansi.StringWidth(m.titleLine(200))
	for w := 10; w <= full+2; w++ {
		line := m.titleLine(w)
		if got := ansi.StringWidth(line); got > w {
			t.Errorf("at width %d the title line %q is %d wide", w, ansi.Strip(line), got)
		}
		has := strings.Contains(ansi.Strip(line), "overnight")
		if want := w >= full; has != want {
			t.Errorf("at width %d (the whole line is %d) the title line %q has the badge: %v", w, full, ansi.Strip(line), has)
		}
	}
	if v := ansi.Strip(m.titleLine(full - 1)); !strings.Contains(v, "main") {
		t.Errorf("title line %q one column short of the badge, want the branch kept", v)
	}
}
