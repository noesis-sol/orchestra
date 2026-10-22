package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// A confirm's answer is in brackets, so it shows without colour: NO_COLOR strips bold and reverse too.

func TestTheFocusedButtonShowsWithoutColour(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	lipgloss.SetColorProfile(termenv.Ascii)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
	for _, tc := range []struct {
		answer  bool
		focused bool
		want    string
	}{
		{true, true, " [Yes]    No  "},
		{false, true, "  Yes    [No] "},
		{true, false, " [Yes]    No  "}, // left: the answer given
		{false, false, "  Yes    [No] "},
	} {
		answer := tc.answer
		confirm := huh.NewConfirm().Title("Start anyway?").Affirmative("Yes").Negative("No").Value(&answer)
		confirm.WithTheme(formTheme())
		if tc.focused {
			confirm.Focus()
		} else {
			confirm.Blur()
		}
		view := confirm.View()
		if !strings.Contains(view, tc.want) {
			t.Errorf("answer %v, focused %v: the buttons aren't %q:\n%s", tc.answer, tc.focused, tc.want, view)
		}
	}
}

func TestTheFocusedButtonKeepsItsWidth(t *testing.T) {
	theme := formTheme()
	focused := lipgloss.Width(theme.Focused.FocusedButton.Render("Yes"))
	if blurred := lipgloss.Width(theme.Focused.BlurredButton.Render("Yes")); focused != blurred {
		t.Errorf("the focused button is %d columns, the other %d", focused, blurred)
	}
}
