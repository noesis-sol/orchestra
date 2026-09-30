package command

import (
	"strings"
	"testing"
)

func TestShortArgsCutsLongArguments(t *testing.T) {
	got := shortArgs([]string{"agent", "start", strings.Repeat("x", 100) + "\nmore"})
	if len([]rune(got)) > 80 || strings.Contains(got, "\n") {
		t.Errorf("got %q", got)
	}
}
