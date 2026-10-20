package tui

import (
	"strings"
	"testing"
)

func TestInitHeaderClearsTheScreenOnATerminal(t *testing.T) {
	var b strings.Builder
	InitScreen{out: &b, width: 80, clear: true}.Header("/repo")
	if !strings.HasPrefix(b.String(), clearScreen) {
		t.Fatalf("header on a terminal = %q, want it to start with the clear sequence %q", b.String(), clearScreen)
	}
	if strings.Count(b.String(), clearScreen) != 1 {
		t.Errorf("header on a terminal = %q, want one clear sequence", b.String())
	}
	if !strings.Contains(strings.SplitN(b.String(), "\n", 2)[0], "init") {
		t.Errorf("header on a terminal = %q, want the title on the first line", b.String())
	}
}

func TestInitHeaderLeavesAPlainWriterUncleared(t *testing.T) {
	var b strings.Builder
	NewInitScreen(&b, ttyTerm).Header("/repo")
	if strings.Contains(b.String(), "\x1b[2J") || strings.Contains(b.String(), "\x1b[H") {
		t.Errorf("header on a plain writer = %q, want no clear sequence", b.String())
	}
	if !strings.Contains(b.String(), "init") {
		t.Errorf("header on a plain writer = %q, want the title", b.String())
	}
}

func TestInitClearsTheScreenOnlyOnATerminalThatIsntDumb(t *testing.T) {
	for _, tc := range []struct {
		onTerminal bool
		term       string
		want       bool
	}{
		{true, "xterm-256color", true},
		{true, "", true},
		{true, "dumb", false},
		{false, "xterm-256color", false},
		{false, "dumb", false},
	} {
		if got := clearsScreen(tc.onTerminal, tc.term); got != tc.want {
			t.Errorf("clearsScreen(%t, %q) = %t, want %t", tc.onTerminal, tc.term, got, tc.want)
		}
	}
}
