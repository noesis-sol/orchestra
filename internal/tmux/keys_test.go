package tmux

import (
	"testing"

	"github.com/noesis-sol/orchestra/internal/hookterm"
)

func TestKeyNamesSpellEveryKey(t *testing.T) {
	for _, k := range hookterm.Keys {
		if name, ok := keyNames.Name(k); !ok || name == "" {
			t.Errorf("tmux has no name for %s", k)
		}
	}
	for k, want := range map[string]string{
		"enter": "Enter", "Return": "Enter", "esc": "Escape", "shift+tab": "BTab", "pagedown": "NPage",
		"ctrl+c": "C-c", "Ctrl+C": "C-c", "alt+x": "M-x",
	} {
		if got, ok := keyNames.Name(k); !ok || got != want {
			t.Errorf("keyNames.Name(%q) = %q, %t; want %q", k, got, ok, want)
		}
	}
	if got, ok := keyNames.Name("hello"); ok {
		t.Errorf("text is typed as it is, not pressed: %q", got)
	}
}
