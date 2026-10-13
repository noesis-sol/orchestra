package cmux

import (
	"strings"
	"unicode/utf8"
)

// keyNames are Herdr's names of the keys SendKeys presses, and cmux's for them.
var keyNames = map[string]string{
	"enter": "enter", "return": "enter", "esc": "escape", "escape": "escape", "tab": "tab",
	"shift+tab": "shift+tab", "space": "space", "backspace": "backspace", "delete": "delete",
	"up": "up", "down": "down", "left": "left", "right": "right",
	"home": "home", "end": "end", "pageup": "pageup", "pagedown": "pagedown",
}

// cmuxKey is cmux's name for Herdr's key name k, and false for text to type as it is: ctrl+c and
// alt+x keep their names.
func cmuxKey(k string) (string, bool) {
	if name, ok := keyNames[strings.ToLower(k)]; ok {
		return name, true
	}
	for _, prefix := range []string{"ctrl+", "alt+"} {
		if rest, ok := strings.CutPrefix(strings.ToLower(k), prefix); ok && utf8.RuneCountInString(rest) == 1 {
			return prefix + rest, true
		}
	}
	return "", false
}
