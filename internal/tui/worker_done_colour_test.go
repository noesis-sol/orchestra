package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

func TestDoneWorkerReadsPlainGreen(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch", Concurrency: 3}, func() {}, func(bool) {}, func(string) {})
	m.active = map[string]dispatch.Status{}
	for i, state := range []dispatch.AgentState{dispatch.StateDone, dispatch.StateWorking, dispatch.StateIdle} {
		id := fmt.Sprintf("kinieta-w%d", i)
		m.active[id] = dispatch.Status{Ticket: id, Title: "Competing timelines", Started: time.Now(), Agent: state}
	}
	done := doneStyle.Render("done")
	if done == closedStyle.Render("done") || done == deferredStyle.Render("done") {
		t.Fatalf("done should be plain green, neither bold like a completed row nor set-aside yellow: %q", done)
	}
	for _, c := range []struct {
		layout string
		height int
		boxes  int // the totals', the tickets table's and the workers'
	}{{"a box per worker", 40, 5}, {"one line per worker", 16, 2}} {
		m.width, m.height = 70, c.height
		v := m.View()
		if strings.Count(ansi.Strip(v), "╭") != c.boxes {
			t.Fatalf("expected %s:\n%s", c.layout, ansi.Strip(v))
		}
		for _, want := range []string{done, pickedStyle.Render("working"), deferredStyle.Render("idle")} {
			if !strings.Contains(v, want) {
				t.Errorf("with %s the view lacks %q:\n%q", c.layout, want, v)
			}
		}
	}
}
