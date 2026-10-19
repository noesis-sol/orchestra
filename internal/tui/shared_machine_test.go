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

// While other orchestra runs share the machine, the title line says how many, in the deferred colour,
// and says no more of it once they have ended; while a free slot waits for the machine's cap on
// workers, it says the machine is full, until a ticket starts or the queue says it no longer waits.
// The lines print as the usage limit's do.
func TestTitleLineShowsTheSharedMachine(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	m := NewDashboard(dispatch.Config{Base: "main", Concurrency: 2}, func() {}, func(bool) {}, func(string) {})
	m.width, m.height = 120, 30
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvShared, Shared: 1,
		Text: "SHARED_MACHINE: another orchestra run is going in /src/tutti with 4 workers; their checks slow each other down"})
	if line, want := m.titleLine(m.width), deferredStyle.Render("  · shared with 1 run"); !strings.Contains(line, want) {
		t.Errorf("title line %q lacks %q", line, want)
	}
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvShared, Shared: 2})
	if v := ansi.Strip(m.titleLine(m.width)); !strings.Contains(v, "· shared with 2 runs") {
		t.Errorf("title line %q, with two other runs", v)
	}

	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "A ticket"},
		dispatch.Event{Kind: dispatch.EvMachine, Text: "HOLD: MACHINE: …"},
		dispatch.Event{Kind: dispatch.EvQueue, Queued: 1, MachineFull: true})
	if line, want := m.titleLine(m.width), deferredStyle.Render("  · machine full"); !strings.Contains(line, want) {
		t.Errorf("title line %q lacks %q", line, want)
	}
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "k-2", Title: "Another"})
	if v := ansi.Strip(m.titleLine(m.width)); strings.Contains(v, "machine full") {
		t.Errorf("title line %q still says the machine is full once a ticket started", v)
	}

	m = runEvents(m, dispatch.Event{Kind: dispatch.EvShared, Shared: 0, Text: "  the other orchestra run in /src/tutti has ended"})
	if v := ansi.Strip(m.titleLine(m.width)); strings.Contains(v, "shared") || strings.Contains(v, "stopping") {
		t.Errorf("title line %q once the other runs ended", v)
	}

	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.Local)
	for _, c := range []struct {
		ev   dispatch.Event
		want string
	}{
		{dispatch.Event{Time: now, Kind: dispatch.EvShared, Shared: 1, Text: "SHARED_MACHINE: another orchestra run is going"},
			"12:00:00 ■ SHARED_MACHINE: another orchestra run is going"},
		{dispatch.Event{Time: now, Kind: dispatch.EvShared, Text: "  the other orchestra run in /src/tutti has ended"},
			"12:00:00 ■ the other orchestra run in /src/tutti has ended"},
		{dispatch.Event{Time: now, Kind: dispatch.EvMachine, Text: "HOLD: MACHINE: at the cap of 8"},
			"12:00:00 ■ HOLD: MACHINE: at the cap of 8"},
	} {
		if line := ansi.Strip(renderEvent(c.ev)); line != c.want {
			t.Errorf("line %q, want %q", line, c.want)
		}
	}
}
