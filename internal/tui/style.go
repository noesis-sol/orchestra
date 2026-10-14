package tui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Exact colours rather than the 16 ANSI palette slots, which terminal themes remap (one theme
// draws "cyan" as near-white). Each has a variant for light and for dark backgrounds.
var (
	cyan   = lipgloss.AdaptiveColor{Light: "#0E7490", Dark: "#22D3EE"}
	green  = lipgloss.AdaptiveColor{Light: "#15803D", Dark: "#4ADE80"}
	yellow = lipgloss.AdaptiveColor{Light: "#A16207", Dark: "#FACC15"}
	red    = lipgloss.AdaptiveColor{Light: "#B91C1C", Dark: "#F87171"}
	amber  = lipgloss.AdaptiveColor{Light: "#C2410C", Dark: "#FB923C"} // the run winding down, as asked
	grey   = lipgloss.AdaptiveColor{Light: "#9CA3AF", Dark: "#6B7280"}
	purple = lipgloss.AdaptiveColor{Light: "#6D28D9", Dark: "#A78BFA"}
	lilac  = lipgloss.AdaptiveColor{Light: "#7C3AED", Dark: "#C4B5FD"} // a lighter purple, for text
	blue   = lipgloss.AdaptiveColor{Light: "#1D4ED8", Dark: "#60A5FA"}
	pink   = lipgloss.AdaptiveColor{Light: "#BE185D", Dark: "#F472B6"}
	chalk  = lipgloss.AdaptiveColor{Light: "#374151", Dark: "#E5E7EB"} // nearer the text's own colour than faint
	// The Charm purple from the Bubble Tea and Lip Gloss examples, the same on any background.
	charmPurple = lipgloss.Color("#7D56F4")
)

var (
	dimStyle      = lipgloss.NewStyle().Faint(true)
	keyStyle      = lipgloss.NewStyle().Foreground(chalk).Bold(true) // a key to press
	ticketsStyle  = lipgloss.NewStyle().Foreground(chalk).Bold(true) // the Tickets label, apart from Current's cyan
	pickedStyle   = lipgloss.NewStyle().Foreground(cyan).Bold(true)  // picked up
	closedStyle   = lipgloss.NewStyle().Foreground(green).Bold(true) // completed
	deferredStyle = lipgloss.NewStyle().Foreground(yellow)           // set aside
	stopStyle     = lipgloss.NewStyle().Foreground(red).Bold(true)   // needs you
	windDownStyle = lipgloss.NewStyle().Foreground(amber)            // winding down: the maintainer's choice, not a need
	doneStyle     = lipgloss.NewStyle().Foreground(green)
	organStyle    = lipgloss.NewStyle().Foreground(purple).Bold(true) // an organ's output
	sayStyle      = lipgloss.NewStyle().Foreground(lilac)             // the organ phase's progress
	testingStyle  = lipgloss.NewStyle().Foreground(yellow).Bold(true) // a worker running checks
	toMergeStyle  = lipgloss.NewStyle().Foreground(blue)              // finished, waiting for its turn to merge
	mergingStyle  = lipgloss.NewStyle().Foreground(pink).Bold(true)   // its merge under way
	// The Charm purple pill from the Bubble Tea and Lip Gloss examples.
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FAFAFA")).
			Background(charmPurple).Padding(0, 1).MarginTop(1)
	home, _ = os.UserHomeDir() // none known: paths are shown in full
)

// Tildify shortens paths under the home directory for display; the log keeps full paths.
func Tildify(s string) string {
	if home == "" {
		return s
	}
	return strings.ReplaceAll(s, home+"/", "~/")
}
