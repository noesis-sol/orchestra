package dispatch

import (
	"strings"
	"testing"
)

// A screen's last lines leave out the empty lines below its last one, and end in a newline.
func TestLastLinesOfAScreen(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("line\n", ScreenLines+10)
	for _, c := range []struct {
		name, s string
		n       int
		want    string
	}{
		{"none", "", 3, ""},
		{"only empty lines", "\n\n\n", 3, ""},
		{"no newline at its end", "a\nb", 3, "a\nb\n"},
		{"empty lines below", "a\n\nb\n\n\n", 3, "a\n\nb\n"},
		{"too many", "a\nb\nc\nd\n", 2, "c\nd\n"},
		{"a line of spaces kept", "a\n  \n", 3, "a\n  \n"},
		{"a long screen", long, ScreenLines, strings.Repeat("line\n", ScreenLines)},
	} {
		if got := LastLines(c.s, c.n); got != c.want {
			t.Errorf("%s: LastLines(%q, %d) = %q, want %q", c.name, c.s, c.n, got, c.want)
		}
	}
}
