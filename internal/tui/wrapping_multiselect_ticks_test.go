package tui

import (
	"slices"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// ticked gives the lines of the form's view that show a ticked option.
func ticked(form interface{ View() string }) []string {
	var lines []string
	for l := range strings.SplitSeq(ansi.Strip(form.View()), "\n") {
		if strings.Contains(l, "✓ ") {
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	return lines
}

func TestMCPOptionsKeepTheirTicksWhenTheyWrapAgain(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []tea.KeyMsg
	}{
		{"unticked", []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune("x")}}},
		{"all, then none", []tea.KeyMsg{{Type: tea.KeyCtrlA}, {Type: tea.KeyCtrlA}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			servers := []string{"postgres"}
			form, field := mcpForm(100, &servers)
			for hovered, _ := field.Hovered(); hovered != "postgres"; hovered, _ = field.Hovered() {
				form.Update(tea.KeyMsg{Type: tea.KeyDown})
			}
			for _, k := range tc.keys {
				form.Update(k)
			}
			if got := ticked(form); len(got) != 0 {
				t.Fatalf("before the resize, ticked %q", got)
			}
			form.Update(tea.WindowSizeMsg{Width: 60, Height: 40})
			if got := ticked(form); len(got) != 0 {
				t.Errorf("after the resize, ticked %q", got)
			}
			field.Blur() // leaving the field sets the value from the ticks
			if !slices.Equal(servers, []string{}) {
				t.Errorf("after the resize and leaving the field, selected %v", servers)
			}
		})
	}
}
