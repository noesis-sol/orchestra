package tui

import (
	"io"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/project"
)

// A select left on the init form shows its answer ticked; a field that isn't focused shows no cursor.

func TestASelectLeftShowsItsAnswerTicked(t *testing.T) {
	c := project.Choice{Concurrent: 1, Servers: testServers}
	term := askOn(t, func(in io.Reader, out io.Writer) error {
		return AskInit(in, out, &c, Ask{Concurrent: true, MCP: true})
	})
	term.waitFor(t, "> 1  · one at a time")
	term.waitFor(t, "exa  · user scope")
	if screen := term.screen.String(); strings.Contains(screen, "> •") || strings.Contains(screen, "✓ 1") {
		t.Errorf("the MCP servers, not reached yet, show a cursor, or tickets at the same time a tick:\n%s", screen)
	}
	term.typeKeys(t, "\x1b[B")
	term.waitFor(t, "> 2  · checks must cope")
	term.typeKeys(t, "\r")
	term.waitFor(t, "✓ 2  · checks must cope")
	term.typeSteps(t, []keysOn{{"> • exa", "\r"}})
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
	if c.Concurrent != 2 {
		t.Errorf("concurrent %d, want 2", c.Concurrent)
	}
}

func TestStepperMarksTheStagesDoneCurrentAndToCome(t *testing.T) {
	for _, tc := range []struct {
		names []string
		n     int
		want  string
	}{
		{[]string{"Workers", "Checks"}, 1, "● Workers ──── ○ Checks"},
		{[]string{"Workers", "Checks"}, 2, "✓ Workers ──── ● Checks"},
		{[]string{"One", "Two", "Three"}, 2, "✓ One ──── ● Two ──── ○ Three"},
		{[]string{"Checks"}, 1, "Checks"},
	} {
		if got := ansi.Strip(stepper(tc.names, tc.n)); got != tc.want {
			t.Errorf("stepper(%v, %d) = %q, want %q", tc.names, tc.n, got, tc.want)
		}
	}
}
