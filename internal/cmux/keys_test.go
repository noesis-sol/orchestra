package cmux

import (
	"testing"

	"github.com/noesis-sol/orchestra/internal/hookterm"
)

func TestKeyNamesSpellEveryKey(t *testing.T) {
	for _, k := range hookterm.Keys {
		if name, ok := keyNames.Name(k); !ok || name == "" {
			t.Errorf("cmux has no name for %s", k)
		}
	}
	for k, want := range map[string]string{
		"enter": "enter", "Return": "enter", "esc": "escape", "shift+tab": "shift+tab", "pagedown": "pagedown",
		"ctrl+c": "ctrl+c", "Ctrl+C": "ctrl+c", "alt+x": "alt+x",
	} {
		if got, ok := keyNames.Name(k); !ok || got != want {
			t.Errorf("keyNames.Name(%q) = %q, %t; want %q", k, got, ok, want)
		}
	}
	if got, ok := keyNames.Name("hello"); ok {
		t.Errorf("text is typed as it is, not pressed: %q", got)
	}
}
