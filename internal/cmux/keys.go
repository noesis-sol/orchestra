package cmux

import "github.com/noesis-sol/orchestra/internal/hookterm"

// keyNames are cmux's names of the keys SendKeys presses, by Herdr's: ctrl+c and alt+x keep their
// names.
var keyNames = hookterm.KeyNames{Ctrl: "ctrl+", Alt: "alt+", Names: map[string]string{
	"enter": "enter", "esc": "escape", "tab": "tab", "shift+tab": "shift+tab", "space": "space",
	"backspace": "backspace", "delete": "delete", "up": "up", "down": "down", "left": "left", "right": "right",
	"home": "home", "end": "end", "pageup": "pageup", "pagedown": "pagedown",
}}
