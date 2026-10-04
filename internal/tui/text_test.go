package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestWordWrapKeepsHyphenatedWords(t *testing.T) {
	got := wordWrap("moved .claude/worker-prompt.md to .orchestra/worker-prompt.md", 30)
	for _, l := range got {
		if strings.HasSuffix(l, "-") || ansi.StringWidth(l) > 30 {
			t.Errorf("bad line %q in %q", l, got)
		}
	}
	if strings.Join(got, " ") != "moved .claude/worker-prompt.md to .orchestra/worker-prompt.md" {
		t.Errorf("words lost: %q", got)
	}
	long := wordWrap(strings.Repeat("x", 25), 10)
	if len(long) != 3 || long[0] != strings.Repeat("x", 10) {
		t.Errorf("a long word should be cut: %q", long)
	}
}
