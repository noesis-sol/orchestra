package tui

import (
	"slices"
	"testing"
	"time"
	"unicode/utf8"
)

// A character wider than the width gets a line of its own rather than wrapping forever.
func TestWordWrapCharacterWiderThanTheWidth(t *testing.T) {
	done := make(chan []string, 1)
	go func() { done <- wordWrap("漢字 a", 1) }()
	select {
	case got := <-done:
		if want := []string{"漢", "字", "a"}; !slices.Equal(got, want) {
			t.Errorf("wordWrap = %q, want %q", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wordWrap didn't return")
	}
	for _, width := range []int{0, -1} {
		if got := wordWrap("ab", width); len(got) == 0 {
			t.Errorf("wordWrap(%d) = %q", width, got)
		}
	}
}

// capitalize upper-cases a first letter that takes more than a byte, leaving valid UTF-8.
func TestCapitalizeMultibyteFirstLetter(t *testing.T) {
	for in, want := range map[string]string{"étude": "Étude", "abc": "Abc", "": "", "1a": "1a", "ß": "ß"} {
		if got := capitalize(in); got != want || !utf8.ValidString(got) {
			t.Errorf("capitalize(%q) = %q, want %q", in, got, want)
		}
	}
}
