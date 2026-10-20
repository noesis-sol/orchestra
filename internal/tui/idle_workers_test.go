package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// With no worker running, the load line's count reads "▶ 0 of 4 workers" all in the faint grey, as the
// strip's zeros do, so it doesn't draw the eye; a running worker keeps its count bright beside a faint
// " of 4 workers".
func TestIdleWorkersAreFaint(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
	f := run(t).config(dispatch.Config{Limit: 40, Concurrency: 4}).sized(120, 40)
	m := f.dash()

	line := m.loadLine(120)
	if faint := dimStyle.Render("▶ 0 of 4 workers"); faint == "▶ 0 of 4 workers" || !strings.Contains(line, faint) {
		t.Errorf("the load line doesn't read a faint \"▶ 0 of 4 workers\": %q", line)
	}

	m = f.running(dispatch.Status{Ticket: "bd-1", Title: "One"}).dash()
	line = m.loadLine(120)
	if !strings.Contains(line, pickedStyle.Render("▶ 1")+dimStyle.Render(" of 4 workers")) {
		t.Errorf("one worker running isn't a bright 1 and a faint \" of 4 workers\": %q", line)
	}
}
