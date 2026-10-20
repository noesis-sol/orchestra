package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	addedStyle   = lipgloss.NewStyle().Foreground(green)
	removedStyle = lipgloss.NewStyle().Foreground(red)
)

// Diff prints a unified diff, as orchestra init --update shows what it changed, indented under the
// steps: its added lines green, its removed ones red, its file and hunk headers dim.
func (u InitScreen) Diff(diff string) {
	if diff == "" {
		return
	}
	fmt.Fprintln(u.out)
	for line := range strings.SplitSeq(strings.TrimSuffix(diff, "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "--- "), strings.HasPrefix(line, "+++ "), strings.HasPrefix(line, "@@"):
			line = dimStyle.Render(line)
		case strings.HasPrefix(line, "+"):
			line = addedStyle.Render(line)
		case strings.HasPrefix(line, "-"):
			line = removedStyle.Render(line)
		}
		fmt.Fprintln(u.out, "    "+line)
	}
}
