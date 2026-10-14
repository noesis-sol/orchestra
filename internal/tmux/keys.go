package tmux

import "github.com/noesis-sol/orchestra/internal/hookterm"

// keyNames are tmux's names of the keys SendKeys presses, by Herdr's: ctrl+c is C-c and alt+x M-x.
var keyNames = hookterm.KeyNames{Ctrl: "C-", Alt: "M-", Names: map[string]string{
	"enter": "Enter", "esc": "Escape", "tab": "Tab", "shift+tab": "BTab", "space": "Space", "backspace": "BSpace",
	"delete": "DC", "up": "Up", "down": "Down", "left": "Left", "right": "Right", "home": "Home", "end": "End",
	"pageup": "PPage", "pagedown": "NPage",
}}
