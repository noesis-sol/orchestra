package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// With no worker running, the Workers box reads "0 of 4" all in the faint grey, as the strip's other
// zeros do, so it doesn't draw the eye; a running worker keeps its count bright beside a faint " of 4".
func TestIdleWorkersAreFaint(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
	m := NewDashboard(dispatch.Config{Limit: 40, Concurrency: 4}, func() {}, func(bool) {}, func(string) {})
	m.width, m.height = 120, 40

	table := m.statsTable(120)
	if faint := dimStyle.Render("0 of 4"); faint == "0 of 4" || !strings.Contains(table, faint) {
		t.Errorf("the Workers box doesn't read a faint \"0 of 4\": %q", table)
	}
	if strings.Contains(table, pickedStyle.Render("0")) {
		t.Errorf("the Workers box shows a bright 0: %q", table)
	}

	m.active = map[string]dispatch.Status{"bd-1": {Ticket: "bd-1", Title: "One"}}
	table = m.statsTable(120)
	if !strings.Contains(table, pickedStyle.Render("1")+dimStyle.Render(" of 4")) {
		t.Errorf("one worker running isn't a bright 1 and a faint \" of 4\": %q", table)
	}
}
