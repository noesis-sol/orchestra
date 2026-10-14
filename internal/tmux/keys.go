package tmux

import (
	"strings"
	"unicode/utf8"
)

// keyNames are Herdr's names of the keys SendKeys presses, and tmux's for them.
var keyNames = map[string]string{
	"enter": "Enter", "return": "Enter", "esc": "Escape", "escape": "Escape", "tab": "Tab", "shift+tab": "BTab",
	"space": "Space", "backspace": "BSpace", "delete": "DC", "up": "Up", "down": "Down", "left": "Left", "right": "Right",
	"home": "Home", "end": "End", "pageup": "PPage", "pagedown": "NPage",
}

// tmuxKey is tmux's name for Herdr's key name k, and false for text to type as it is: ctrl+c is
// C-c and alt+x M-x.
func tmuxKey(k string) (string, bool) {
	if name, ok := keyNames[strings.ToLower(k)]; ok {
		return name, true
	}
	for prefix, mod := range map[string]string{"ctrl+": "C-", "alt+": "M-"} {
		if rest, ok := strings.CutPrefix(strings.ToLower(k), prefix); ok && utf8.RuneCountInString(rest) == 1 {
			return mod + rest, true
		}
	}
	return "", false
}
