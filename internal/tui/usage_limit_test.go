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

// While the run holds for the usage limit, the title line says so in the deferred colour, with when
// the limit resets when that is known, and says no more of it once the hold lifts; the run isn't
// stopping meanwhile. The plain output prints the hold and its lifting.
func TestTitleLineShowsTheUsageLimit(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.Local)
	m := NewDashboard(dispatch.Config{Base: "main", Concurrency: 2}, func() {}, func(bool) {}, func(string) {})
	m.now = func() time.Time { return now }
	m.width, m.height = 120, 30
	reset := time.Date(2026, 10, 16, 14, 5, 0, 0, time.Local)
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "A ticket"},
		dispatch.Event{Kind: dispatch.EvLimited, Ticket: "k-1", Reset: reset,
			Text: "USAGE_LIMIT: k-1's worker reached the usage limit; it resets at 14:05"})
	if line, want := m.titleLine(m.width), deferredStyle.Render("  · usage limit · resets 14:05"); !strings.Contains(line, want) {
		t.Errorf("title line %q lacks %q", line, want)
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "Orchestra") || !strings.Contains(v, "usage limit · resets 14:05") ||
		strings.Contains(v, "stopping") {
		t.Errorf("view:\n%s", v)
	}

	m = runEvents(m, dispatch.Event{Kind: dispatch.EvLimitLift, Text: "USAGE_LIMIT lifted: the limit reset at 14:05"})
	if v := ansi.Strip(m.titleLine(m.width)); strings.Contains(v, "usage limit") {
		t.Errorf("title line %q still says the usage limit once lifted", v)
	}

	m = runEvents(m, dispatch.Event{Kind: dispatch.EvLimited, Ticket: "k-1",
		Text: "USAGE_LIMIT: k-1's worker reached the usage limit; when it resets is not known"})
	if v := ansi.Strip(m.titleLine(m.width)); !strings.Contains(v, "· usage limit") || strings.Contains(v, "resets") {
		t.Errorf("title line %q, with no reset known", v)
	}

	tomorrow := reset.AddDate(0, 0, 1)
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvLimited, Ticket: "k-1", Reset: tomorrow})
	if v, want := ansi.Strip(m.titleLine(m.width)), "usage limit · resets "+tomorrow.Format("Mon Jan 2")+" 14:05"; !strings.Contains(v, want) {
		t.Errorf("title line %q lacks %q", v, want)
	}

	for _, c := range []struct {
		ev   dispatch.Event
		want string
	}{
		{dispatch.Event{Time: now, Kind: dispatch.EvLimited, Ticket: "k-1",
			Text: "USAGE_LIMIT: k-1's worker reached the usage limit; it resets at 14:05"},
			"12:00:00 ■ USAGE_LIMIT: k-1's worker reached the usage limit; it resets at 14:05"},
		{dispatch.Event{Time: now, Kind: dispatch.EvLimitLift, Text: "USAGE_LIMIT lifted: the limit reset at 14:05"},
			"12:00:00 ■ USAGE_LIMIT lifted: the limit reset at 14:05"},
	} {
		if line := ansi.Strip(renderEvent(c.ev)); line != c.want {
			t.Errorf("line %q, want %q", line, c.want)
		}
	}
}
