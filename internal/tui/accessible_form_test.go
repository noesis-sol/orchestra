package tui

import (
	"context"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/project"
)

// With TERM=dumb, huh asks a form's questions one after another as plain lines (its accessible mode).
// A hidden question isn't asked there either, and a shown one still is.

// typed is the answers to an accessible form, a byte per read, as a terminal hands over a line at a
// time: huh reads each answer with a scanner of its own, which would otherwise take every line.
func typed(lines ...string) io.Reader {
	return iotest.OneByteReader(strings.NewReader(strings.Join(lines, "\n") + "\n"))
}

func TestAccessibleWorkFormAsksForADescriptionOnlyForAFeature(t *testing.T) {
	t.Setenv("TERM", "dumb")
	for _, tc := range []struct {
		name    string
		answers []string
		want    string
		asked   bool
	}{
		{"current tickets", []string{"1"}, "", false},
		{"new feature", []string{"2", "", "  Export the run report  "}, "Export the run report", true},
	} {
		var out strings.Builder
		got, err := AskWork(context.Background(), typed(tc.answers...), &out, 3, nil)
		screen := ansi.Strip(out.String())
		if err != nil || got != tc.want {
			t.Errorf("%s: AskWork = %q, %v; want %q\n%s", tc.name, got, err, tc.want, screen)
		}
		if asked := strings.Contains(screen, "Describe the feature"); asked != tc.asked {
			t.Errorf("%s: asked for a description: %v, want %v\n%s", tc.name, asked, tc.asked, screen)
		}
	}
}

func TestAccessibleInitFormAsksForANumberOnlyForCustom(t *testing.T) {
	t.Setenv("TERM", "dumb")
	for _, tc := range []struct {
		name    string
		answers []string // tickets at the same time, the number typed when asked, union
		want    int
		asked   bool
	}{
		{"2", []string{"2", "y"}, 2, false},
		{"Custom… 6", []string{"5", "", "6", "y"}, 6, true},
	} {
		c := project.Choice{Concurrent: 1}
		var out strings.Builder
		err := AskInit(typed(tc.answers...), &out, &c, false, false, true, true, false, false)
		screen := ansi.Strip(out.String())
		if err != nil || c.Concurrent != tc.want || !c.Union {
			t.Errorf("%s: AskInit: %v, concurrent %d, union %v; want %d, true\n%s",
				tc.name, err, c.Concurrent, c.Union, tc.want, screen)
		}
		if asked := strings.Contains(screen, "Number of tickets"); asked != tc.asked {
			t.Errorf("%s: asked for a number: %v, want %v\n%s", tc.name, asked, tc.asked, screen)
		}
	}
}
