package tui

import "github.com/charmbracelet/huh"

// formTheme is huh's Charm theme with the focused button, a confirm's answer, in brackets: the theme
// tells it from the others only by colour, which NO_COLOR strips, bold and reverse with it. The
// brackets take one column of the padding on each side, so the button keeps its width.
func formTheme() *huh.Theme {
	theme := huh.ThemeCharm()
	for _, styles := range []*huh.FieldStyles{&theme.Focused, &theme.Blurred} {
		styles.FocusedButton = styles.FocusedButton.
			PaddingLeft(styles.FocusedButton.GetPaddingLeft() - 1).
			PaddingRight(styles.FocusedButton.GetPaddingRight() - 1).
			Transform(func(s string) string { return "[" + s + "]" })
	}
	return theme
}
