package project

import (
	"strings"
	"testing"
)

// A breakdown, in every mode, has a ticket that changes part of a Markdown doc name the section it
// changes, in its text and its files metadata, so that tickets on different sections of one doc run
// side by side.
func TestBreakdownTicketsNameTheDocSectionTheyChange(t *testing.T) {
	want := []string{
		"Where a ticket changes part of a Markdown doc, cite the section it changes rather than the whole " +
			`doc (docs/guide.md § "Install", or docs/guide.md#install in its files)`,
		"orchestra keeps two tickets on one doc apart only when they name the same section, or one names " +
			"the doc without a section",
		"a Markdown doc of which it changes a part as the doc and its section's anchor, `docs/guide.md#install`",
	}
	for name, write := range map[string]func(repo string) (string, error){
		"headless":        func(repo string) (string, error) { return WriteSpecPrompt(repo, false) },
		"headless update": func(repo string) (string, error) { return WriteSpecPrompt(repo, true) },
		"refinement":      func(repo string) (string, error) { return WriteRefinePrompt(repo, RefineSpec) },
		"refined update on the terminal": func(repo string) (string, error) {
			return WriteTerminalRefine(repo, RefineUpdate)
		},
		"feature": func(repo string) (string, error) { return WriteRefinePrompt(repo, RefineFeature) },
	} {
		t.Run(name, func(t *testing.T) {
			p, err := write(runFolder(t))
			if err != nil {
				t.Fatal(err)
			}
			text := readText(t, p)
			for _, w := range want {
				if !strings.Contains(text, w) {
					t.Errorf("the instructions lack %q", w)
				}
			}
		})
	}
}
