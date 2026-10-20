package claudescreen

import (
	"strings"
	"testing"
)

// TestInputHoldsAPromptWrappedInItsFirstWords: Claude Code wraps its input box at the spaces between
// words, dropping the space, and breaks only a word longer than the box. Joining the box's lines with
// a space gives the prompt back, so a prompt wrapped within the first 40 runes InputHolds compares
// still matches and isn't sent again. The screen is Claude Code 2.1.299's in a pane 44 columns wide.
func TestInputHoldsAPromptWrappedInItsFirstWords(t *testing.T) {
	t.Parallel()
	prompt := "You are responsible for exactly one Beads ticket: orchestra-74b2.37. Do not work on any other " +
		"ticket. Supercalifragilisticexpialidociousandmorewordsjoinedtogetherwithoutspaces end."
	screen := strings.Join([]string{
		"                        ◐ medium · /effort",
		"────────────────────────────────────────────",
		"❯ You are responsible for exactly one",
		"  Beads ticket: orchestra-74b2.37. Do not",
		"  work on any other ticket.               ",
		"  Supercalifragilisticexpialidociousandmor  ",
		"  ewordsjoinedtogetherwithoutspaces end.",
		"────────────────────────────────────────────",
		"  ⏵⏵ auto mode on (shift+tab to cycle)    ",
	}, "\n")
	if !(Reader{}).InputHolds(screen, prompt) {
		t.Errorf("InputHolds = false for a prompt Claude Code wrapped after %q, want true", "exactly one")
	}
}
