package project

import (
	"slices"
	"strings"
	"testing"
)

// spikesHeading opens the section of the breakdown's instructions on spikes for unverified facts.
const spikesHeading = "## Spikes for unverified facts"

// A spec's breakdown, in every mode, files a spike for each fact the spec marks unverified that
// tickets rest on (or one for a few related facts): a ticket that checks it against the library's
// source, its documentation or a minimal program, says what it found in its close reason, and files
// a question blocking the tickets that rest on a fact it finds false rather than change the spec.
// Spikes come right after the environment ticket and before the tickets that rest on them; a fact
// nothing rests on gets none.
func TestSpecBreakdownFilesSpikesForUnverifiedFacts(t *testing.T) {
	want := []string{
		"Where the spec marks a fact unverified (`[VERIFY]`, \"to verify\", \"check that\", an open question " +
			"about an outside API, library or tool) and tickets rest on it, file a **spike** for it, or one for a " +
			"few related facts",
		"checks the fact against the library's source, its documentation or a minimal program",
		"changes nothing in the repository",
		"to say what it found, and from where, in its close reason",
		"where it finds a fact false, to file a question, the way its instructions say, blocking the " +
			"tickets that rest on it, rather than change the spec or build on a guess",
		"A spike carries the label `spike` and priority 1, no files, and waits for the environment ticket " +
			"where there is one",
		"Each ticket that rests on a fact names it in its description",
		"A fact nothing rests on gets no spike",
	}
	// The headless breakdown gives spikes in its answer; one with the user, or an update, files them.
	const (
		inAnswer = "Spikes come in your answer right after the environment ticket, before the tickets that " +
			"rest on them, and each of those lists its spike's key in its `waits_for`"
		filed = "File spikes right after the environment ticket, before the tickets that rest on them, and " +
			"give each of those a blocks link to its spike"
		update = "In an update, file spikes only for the unverified facts the change adds, or that the " +
			"tickets it files rest on"
	)
	for name, tc := range map[string]struct {
		write    func(repo string) (string, error)
		has, not []string
	}{
		"headless": {func(repo string) (string, error) { return WriteSpecPrompt(repo, false) },
			[]string{inAnswer}, []string{filed, update}},
		"headless update": {func(repo string) (string, error) { return WriteSpecPrompt(repo, true) },
			[]string{filed, update}, []string{inAnswer}},
		"refinement in a pane": {func(repo string) (string, error) { return WriteRefinePrompt(repo, RefineSpec) },
			[]string{filed}, []string{inAnswer, update}},
		"refinement on the terminal": {func(repo string) (string, error) { return WriteTerminalRefine(repo, RefineSpec) },
			[]string{filed}, []string{inAnswer, update}},
		"refined update": {func(repo string) (string, error) { return WriteRefinePrompt(repo, RefineUpdate) },
			[]string{filed, update}, []string{inAnswer}},
		"refined update on the terminal": {
			func(repo string) (string, error) { return WriteTerminalRefine(repo, RefineUpdate) },
			[]string{filed, update}, []string{inAnswer}},
	} {
		write, want, not := tc.write, append(slices.Clone(want), tc.has...), tc.not
		t.Run(name, func(t *testing.T) {
			p, err := write(runFolder(t))
			if err != nil {
				t.Fatal(err)
			}
			text := readText(t, p)
			_, rest, ok := strings.Cut(text, spikesHeading)
			if !ok {
				t.Fatalf("the instructions have no %q", spikesHeading)
			}
			section, _, _ := strings.Cut(rest, " ## ")
			for _, w := range want {
				if !strings.Contains(section, w) {
					t.Errorf("the spikes section lacks %q", w)
				}
			}
			for _, g := range not {
				if strings.Contains(section, g) {
					t.Errorf("the spikes section has %q", g)
				}
			}
			if env, _, _ := strings.Cut(text, spikesHeading); !strings.Contains(env, environmentHeading) {
				t.Errorf("the spikes section comes before %q", environmentHeading)
			}
		})
	}
}

// A feature's breakdown, which has no spec to mark facts unverified, files no spikes.
func TestFeatureBreakdownFilesNoSpikes(t *testing.T) {
	for name, write := range map[string]func(repo string) (string, error){
		"in a pane":       func(repo string) (string, error) { return WriteRefinePrompt(repo, RefineFeature) },
		"on the terminal": func(repo string) (string, error) { return WriteTerminalRefine(repo, RefineFeature) },
	} {
		t.Run(name, func(t *testing.T) {
			p, err := write(runFolder(t))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(readText(t, p), spikesHeading) {
				t.Errorf("the feature's instructions have %q", spikesHeading)
			}
		})
	}
}
