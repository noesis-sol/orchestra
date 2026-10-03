package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A resize clears the screen, so the next frame starts at the top rather than over the middle of
// the last, re-wrapped one; the first size, on a screen cleared already, and the same size again
// don't.
func TestResizeClearsTheScreen(t *testing.T) {
	var m tea.Model = NewDashboard(dispatch.Config{Limit: 40, Concurrency: 4}, func() {}, func(bool) {},
		func(string) {})
	for _, step := range []struct {
		width, height int
		clears        bool
	}{
		{67, 40, false}, // the first size
		{67, 40, false}, // the same again
		{66, 40, true},  // narrower, as when a pane beside it grows
		{66, 30, true},  // shorter
		{80, 30, true},  // wider
	} {
		var cmd tea.Cmd
		m, cmd = m.Update(tea.WindowSizeMsg{Width: step.width, Height: step.height})
		if got := cmd != nil && cmd() == tea.ClearScreen(); got != step.clears {
			t.Errorf("at %dx%d: clears %v, want %v", step.width, step.height, got, step.clears)
		}
		if d := m.(Dashboard); d.width != step.width || d.height != step.height {
			t.Errorf("at %dx%d: the dashboard has %dx%d", step.width, step.height, d.width, d.height)
		}
	}
}
