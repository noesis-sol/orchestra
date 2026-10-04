package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A resize only sizes the dashboard: on the alternate screen a terminal's reflow has no scrollback
// to push rows into, and Bubble Tea draws each frame from the top, so there is nothing to clear (a
// clear's ESC[2J never reached the rows a reflow pushed into the normal screen's scrollback).
func TestResizeOnlySizesTheDashboard(t *testing.T) {
	var m tea.Model = NewDashboard(dispatch.Config{Limit: 40, Concurrency: 4}, func() {}, func(bool) {},
		func(string) {})
	for _, size := range [][2]int{
		{67, 40}, // the first size
		{67, 40}, // the same again
		{66, 40}, // narrower, as when a pane beside it grows
		{66, 30}, // shorter
		{80, 30}, // wider
	} {
		var cmd tea.Cmd
		m, cmd = m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		if cmd != nil {
			t.Errorf("at %dx%d: the resize returned a command (%T), want none", size[0], size[1], cmd())
		}
		if d := m.(Dashboard); d.width != size[0] || d.height != size[1] {
			t.Errorf("at %dx%d: the dashboard has %dx%d", size[0], size[1], d.width, d.height)
		}
	}
}
