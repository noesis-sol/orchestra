package main

import (
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// The terminal a spec run's fetched settings choose is for the next run only where it isn't the one
// the run chose as it started; --terminal overrides it, as it does the settings at configuration, and
// a terminal that isn't one is a setup problem.
func TestFetchedTerminal(t *testing.T) {
	t.Parallel()
	tmux := project.Given[string]{Value: "tmux", Given: true}
	for _, tc := range []struct {
		name, setting, running string
		asked                  project.Given[string]
		want, problem          string
	}{
		{"none", "", "herdr", project.Given[string]{}, "", ""},
		{"auto", "auto", "herdr", project.Given[string]{}, "", ""},
		{"the run's own", "herdr", "herdr", project.Given[string]{}, "", ""},
		{"another", "tmux", "herdr", project.Given[string]{}, "tmux", ""},
		{"overridden by --terminal", "cmux", "tmux", tmux, "", ""},
		{"not a terminal", "screen", "herdr", project.Given[string]{}, "", "x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := options{terminalAsked: tc.asked}
			c.Terminal = tc.running
			got, problem := fetchedTerminal(c, project.Settings{Terminal: tc.setting})
			if got != tc.want || (problem != "") != (tc.problem != "") {
				t.Errorf("got %q, problem %q; want %q, a problem %v", got, problem, tc.want, tc.problem != "")
			}
		})
	}
}
