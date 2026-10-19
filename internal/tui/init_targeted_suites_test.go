package tui

import (
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
)

// The suites chosen keep the scout's command for part of the project, for the worker prompt.
func TestTheChosenSuitesKeepHowToRunThemOnPartOfTheProject(t *testing.T) {
	found := organ.Scouting{Suites: slices.Clone(fourSuites.Suites)}
	found.Suites[1].Targeted = "npx jest <paths>"
	found.Suites[3].Targeted = "npx jest -c integration.config.js <paths>"
	c := project.Choice{}
	term := askChecks(t, &c, Ask{Questions: project.Questions{Check: true}, Scout: finds(found, nil)})
	term.typeSteps(t, []keysOn{{onFound, "\r"}, {onFast, "\r"}, {onFull, "\r"}, {onUntested, "\r"}})
	if err := term.end(t); err != nil {
		t.Fatal(err)
	}
	targeted := func(suites *[]project.Suite) []string {
		var got []string
		for _, s := range *suites {
			got = append(got, s.Targeted)
		}
		return got
	}
	if got, want := targeted(c.Fast), []string{"", "npx jest <paths>"}; !slices.Equal(got, want) {
		t.Errorf("check-fast's %q, want %q", got, want)
	}
	if got, want := targeted(c.Full), []string{"", "npx jest -c integration.config.js <paths>"}; !slices.Equal(got, want) {
		t.Errorf("check-full's %q, want %q", got, want)
	}
}
