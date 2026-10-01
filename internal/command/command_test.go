package command

import (
	"context"
	"strings"
	"testing"
)

func TestShortArgsCutsLongArguments(t *testing.T) {
	got := shortArgs([]string{"agent", "start", strings.Repeat("x", 100) + "\nmore"})
	if len([]rune(got)) > 80 || strings.Contains(got, "\n") {
		t.Errorf("got %q", got)
	}
}

func TestShellQuoteMakesOneWord(t *testing.T) {
	for _, in := range []string{"Your instructions for ticket k-1 are in .orchestra/run/prompt.md.", "it's `a` $test \"q\""} {
		out, err := Output(context.Background(), 0, "", "sh", "-c", "printf '%s' "+ShellQuote(in))
		if err != nil || out != in {
			t.Errorf("ShellQuote(%q) round-trips to %q (%v)", in, out, err)
		}
	}
}
