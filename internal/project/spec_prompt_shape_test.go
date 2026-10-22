package project

import (
	"strings"
	"testing"
)

// sideBySideHeading opens the section of the breakdown's instructions on tickets that run side by side.
const sideBySideHeading = "## Tickets that run side by side"

// A breakdown, in every mode, files a wide tree: blocks links only where a ticket needs the other's
// code, chains three links deep at most, a hub ticket cut to the seam, a file for each ticket meant to
// run beside the others, tests in the ticket whose code they test, a closing docs ticket only where
// the tickets would all edit one doc, and `solo` only where most tickets would conflict; each rule with
// its reason. A feature, which has no environment ticket or spikes, counts its chain without them.
func TestBreakdownShapesATreeThatRunsSideBySide(t *testing.T) {
	want := []string{
		"Keep the tree wide, so that the run keeps its workers busy",
		"A blocks link only where a ticket can't be written until the other's code is on the base: it " +
			"calls, extends or edits code the other adds",
		"Not for an order of preference, docs after code, tests after the feature, or two tickets in one area",
		"orchestra keeps tickets that change the same code apart without a link, and a link costs the " +
			"waiting ticket the other's whole session and its merge",
		"the longest chain of blocks links as short as the work allows, three at most",
		"Four workers stay busy only with about four tickets for each step of the chain",
		"A ticket three or more others build on is cut to the seam: the flag, types, signatures and empty " +
			"registration points the others fill in",
		"with a test that it is wired",
		"It then merges in minutes, and the substance goes to the dependents",
		"Tickets meant to run side by side get a file each, or a section each of a Markdown doc: tickets " +
			"that share a file run one after another, whatever their links say",
		"Tests are part of the ticket whose code they test, never a ticket after it",
		"A closing docs ticket only where the tickets beside each other would otherwise all edit the same " +
			"doc, and nothing waits on it",
		"The label `solo` only for a change most tickets would conflict with",
		"otherwise its files metadata lists what it changes",
	}
	const aside = "three at most, the environment ticket and spikes aside"
	for name, tc := range map[string]struct {
		write   func(repo string) (string, error)
		feature bool
	}{
		"headless":        {func(repo string) (string, error) { return WriteSpecPrompt(repo, false) }, false},
		"headless update": {func(repo string) (string, error) { return WriteSpecPrompt(repo, true) }, false},
		"refinement in a pane": {
			func(repo string) (string, error) { return WriteRefinePrompt(repo, RefineSpec) }, false},
		"refinement on the terminal": {
			func(repo string) (string, error) { return WriteTerminalRefine(repo, RefineSpec) }, false},
		"refined update": {func(repo string) (string, error) { return WriteRefinePrompt(repo, RefineUpdate) }, false},
		"refined update on the terminal": {
			func(repo string) (string, error) { return WriteTerminalRefine(repo, RefineUpdate) }, false},
		"feature in a pane": {
			func(repo string) (string, error) { return WriteRefinePrompt(repo, RefineFeature) }, true},
		"feature on the terminal": {
			func(repo string) (string, error) { return WriteTerminalRefine(repo, RefineFeature) }, true},
	} {
		t.Run(name, func(t *testing.T) {
			p, err := tc.write(runFolder(t))
			if err != nil {
				t.Fatal(err)
			}
			_, rest, ok := strings.Cut(readText(t, p), sideBySideHeading)
			if !ok {
				t.Fatalf("the instructions have no %q", sideBySideHeading)
			}
			section, _, _ := strings.Cut(rest, " ## ")
			for _, w := range want {
				if !strings.Contains(section, w) {
					t.Errorf("the side-by-side section lacks %q", w)
				}
			}
			if got := strings.Contains(section, aside); got == tc.feature {
				t.Errorf("the side-by-side section has %q: %v, want %v", aside, got, !tc.feature)
			}
		})
	}
}

// The tree's tickets point at those rules: a blocks link to each ticket whose code it builds on, and to
// no other.
func TestBreakdownTicketsLinkOnlyToTheCodeTheyBuildOn(t *testing.T) {
	const want = "a blocks link to each ticket whose code it builds on, so it starts only once that one is " +
		"merged, and to no other (see Tickets that run side by side)"
	for name, write := range map[string]func(repo string) (string, error){
		"headless": func(repo string) (string, error) { return WriteSpecPrompt(repo, false) },
		"refined update": func(repo string) (string, error) {
			return WriteRefinePrompt(repo, RefineUpdate)
		},
		"feature": func(repo string) (string, error) { return WriteRefinePrompt(repo, RefineFeature) },
	} {
		t.Run(name, func(t *testing.T) {
			p, err := write(runFolder(t))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(readText(t, p), want) {
				t.Errorf("the instructions lack %q", want)
			}
		})
	}
}
