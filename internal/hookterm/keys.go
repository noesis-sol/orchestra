package hookterm

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// Keys are Herdr's names of the keys SendKeys presses, besides ctrl+ and alt+ with a character: each
// adapter spells each of them its own way.
var Keys = []string{
	"enter", "esc", "tab", "shift+tab", "space", "backspace", "delete",
	"up", "down", "left", "right", "home", "end", "pageup", "pagedown",
}

// aliases are Herdr's other names for some of Keys.
var aliases = map[string]string{"return": "enter", "escape": "esc"}

// Key is a key SendKeys presses: one of Keys, or a character with Ctrl or Alt.
type Key struct {
	Name      string // one of Keys, or the character pressed with Ctrl or Alt
	Ctrl, Alt bool
}

// ParseKey reads k as Herdr's name of a key, whatever its case: enter, esc, ctrl+c, alt+x…; false for
// text to type as it is.
func ParseKey(k string) (Key, bool) {
	k = strings.ToLower(k)
	if name, ok := aliases[k]; ok {
		return Key{Name: name}, true
	}
	if slices.Contains(Keys, k) {
		return Key{Name: k}, true
	}
	if rest, ok := strings.CutPrefix(k, "ctrl+"); ok && utf8.RuneCountInString(rest) == 1 {
		return Key{Name: rest, Ctrl: true}, true
	}
	if rest, ok := strings.CutPrefix(k, "alt+"); ok && utf8.RuneCountInString(rest) == 1 {
		return Key{Name: rest, Alt: true}, true
	}
	return Key{}, false
}

// KeyNames is how a terminal spells the keys SendKeys presses: each of Keys as Names has it, and a
// character pressed with Ctrl or Alt after the prefix Ctrl or Alt.
type KeyNames struct {
	Names     map[string]string // by Herdr's names: one for each of Keys
	Ctrl, Alt string            // what comes before a character pressed with Ctrl, or Alt: C- and M-, say
}

// Name is the terminal's name for Herdr's key name k, and false for text to type as it is.
func (n KeyNames) Name(k string) (string, bool) {
	key, ok := ParseKey(k)
	switch {
	case !ok:
		return "", false
	case key.Ctrl:
		return n.Ctrl + key.Name, true
	case key.Alt:
		return n.Alt + key.Name, true
	}
	return n.Names[key.Name], true
}
