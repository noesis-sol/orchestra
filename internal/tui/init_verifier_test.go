package tui

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
)

const onVerifier = "┃ Add a verification step for workers"

// Stage 2 asks for the verifier last, once the scout has said what the project is built with, and
// the choice keeps the stack for it.
func TestTheVerifierIsAskedWithTheStackTheScoutFound(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want bool
	}{{"\r", true}, {"n", false}} {
		found := fourSuites
		found.Stack = []string{"TypeScript 5", "Playwright"}
		c := project.Choice{Verifier: true} // offered as yes
		term := askChecks(t, &c, Ask{Check: true, Verifier: true, Scout: finds(found, nil)})
		term.typeSteps(t, []keysOn{{onFound, "\r"}, {onFast, "\r"}, {onFull, "\r"}, {onUntested, "\r"},
			{"It is told the stack: TypeScript 5, Playwright.", ""}, {onVerifier, tc.key}})
		if err := term.end(t); err != nil {
			t.Fatalf("%q: %v", tc.key, err)
		}
		if c.Verifier != tc.want || !slices.Equal(c.Stack, found.Stack) {
			t.Errorf("%q: verifier %v, stack %q; want %v and the scout's", tc.key, c.Verifier, c.Stack, tc.want)
		}
	}
}

// With no scouting, the verifier is still asked, with no stack to name.
func TestTheVerifierIsAskedWithoutTheScout(t *testing.T) {
	c := project.Choice{Verifier: true}
	term := askChecks(t, &c, Ask{Check: true, Verifier: true, Scout: finds(organ.Scouting{}, errors.New("no claude"))})
	term.typeSteps(t, []keysOn{{onManual, "\r"}, {onCheck, "make check\r"}, {onVerifier, "\r"}})
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
	if !c.Verifier || len(c.Stack) != 0 {
		t.Errorf("verifier %v, stack %q", c.Verifier, c.Stack)
	}
	if strings.Contains(term.screen.String(), "It is told the stack") {
		t.Error("the question names a stack")
	}
}
