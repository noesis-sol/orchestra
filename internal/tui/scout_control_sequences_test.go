package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
)

// hostile is what a repository could make the scout echo, JSON's \u001b decoding to an ESC: a
// clipboard write, which huh's form drops itself, then a bell and a backspace, which it doesn't.
const hostile = "\x1b]52;c;cm0gLXJmIH4K\x07\a\b"

// hostileScouting is what a scout fooled by a repository's files could find: escape sequences in its
// note, its stack and a suite's fields.
var hostileScouting = organ.Scouting{
	Suites: []organ.Suite{{Name: "unit" + hostile + " tests", Kind: organ.SuiteUnit, Command: "npm" + hostile + " test",
		FoundIn: "package.json" + hostile, Tier: organ.TierFast, ParallelSafe: true, Needs: []string{"db" + hostile}}},
	Note:  "Fix " + hostile + "it.",
	Stack: []string{"Type" + hostile + "Script"},
}

// In accessible mode, the scout's field says what the scout found, and why it found nothing, without
// the escape sequences in its note or its error.
func TestAccessibleScoutDropsControlSequences(t *testing.T) {
	for _, tc := range []struct {
		name  string
		found organ.Scouting
		err   error
	}{
		{"note", hostileScouting, nil},
		{"error", organ.Scouting{}, &organ.ScoutError{Failure: organ.ScoutFailed, Err: errors.New("Fix " + hostile + "it")}},
	} {
		stage := &initStage{}
		addChecks(t.Context(), stage, &project.Choice{}, Ask{Questions: project.Questions{Check: true}, Scout: finds(tc.found, tc.err)}, true)
		var out strings.Builder
		if err := stage.fields[0].RunAccessible(&out, strings.NewReader("")); err != nil {
			t.Fatal(err)
		}
		checkPrintable(t, tc.name, out.String())
	}
}

// The form shows the scout's note, its suites and its stack without their escape sequences, and
// writes the suite's command as the scout gave it.
func TestScoutFormDropsControlSequences(t *testing.T) {
	c := project.Choice{Verifier: true}
	term := askChecks(t, &c, Ask{Questions: project.Questions{Check: true, Verifier: true}, Scout: finds(hostileScouting, nil)})
	term.typeSteps(t, []keysOn{{"The scout found 1 suite. Fix it.", ""},
		{onFound, "\r"}, {"npm test  · unit tests · package.json · needs db", "\r"},
		{onUntested, "\r"}, {"It is told the stack: TypeScript.", ""}, {onVerifier, "\r"}})
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
	if screen := term.screen.String(); strings.ContainsAny(screen, "\a\b") || strings.Contains(screen, "]52;") {
		t.Errorf("a control character came through:\n%q", screen)
	}
	if got := commands(c.Fast); len(got) != 1 || got[0] != "npm"+hostile+" test" {
		t.Errorf("check-fast %q, want the scout's command", got)
	}
}
