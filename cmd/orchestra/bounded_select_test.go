package main

import (
	"context"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Up and Down at the work question, as a terminal sends them: the arrow, k or j, Ctrl+K or Ctrl+J,
// Ctrl+P or Ctrl+N.
var (
	upKeys   = []string{"\x1b[A", "k", "\x0b", "\x10"}
	downKeys = []string{keyDown, "j", keyNewLine, "\x0e"}
)

// Up on the first option leaves it selected, and Enter runs the current tickets.
func TestWorkQuestionStopsAtTheFirstOption(t *testing.T) {
	for _, up := range upKeys {
		term, answer := askWorkOn(context.Background(), t, fakeReady{n: 3})
		term.waitFor(t, "> Current tickets: 3 ready")
		term.typeKeys(t, up+keyEnter, true)
		a := answer()
		if a.description != "" || a.code != dispatch.ExitOK || strings.Contains(a.out, "Describe the feature") {
			t.Errorf("%q then Enter: got %q, exit %d; the screen:\n%s", up, a.description, a.code, a.out)
		}
	}
}

// Down on the last option leaves it selected, and Enter asks for the feature's description.
func TestWorkQuestionStopsAtTheLastOption(t *testing.T) {
	for _, down := range downKeys {
		term, answer := askWorkOn(context.Background(), t, fakeReady{n: 3})
		term.waitFor(t, "> Current tickets: 3 ready")
		term.typeKeys(t, keyDown+down+keyEnter, false)
		term.waitFor(t, describing)
		term.typeKeys(t, "Add a --json flag"+keyEnter, true)
		if a := answer(); a.description != "Add a --json flag" || a.code != dispatch.ExitOK {
			t.Errorf("Down, %q then Enter: got %q, exit %d", down, a.description, a.code)
		}
	}
}

// '/' doesn't filter the options: with "New" typed after it, Enter would pick New feature. The
// help line doesn't offer it.
func TestWorkQuestionDoesNotFilter(t *testing.T) {
	const ctrlA = "\x01" // ends the '/' key; does nothing at the question
	term, answer := askWorkOn(context.Background(), t, fakeReady{n: 3})
	term.waitFor(t, "> Current tickets: 3 ready")
	term.waitFor(t, "↑ up")
	if screen := term.screen.String(); strings.Contains(screen, "filter") {
		t.Errorf("the help line offers the filter key:\n%s", screen)
	}
	term.typeKeys(t, "/"+ctrlA+"New"+keyEnter, true)
	a := answer()
	if a.description != "" || a.code != dispatch.ExitOK || strings.Contains(a.out, "Describe the feature") {
		t.Errorf("got %q, exit %d; the screen:\n%s", a.description, a.code, a.out)
	}
}
