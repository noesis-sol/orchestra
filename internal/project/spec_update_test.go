package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// In update mode, the spec changed since its tree was filed, each breakdown's instructions update that
// tree: they read the change and the tree from spec-update.md, file only what the change needs under
// the root epic, never change a closed issue, defer the open tickets the change makes obsolete with a
// note naming it, and set the root epic's SHA-256 and commit last; a new spec's parts (filing a root
// epic, the scaffold of a project with no code) are gone. The headless ones still answer, the
// refinement's still ask, about the change alone, and hand back.
func TestSpecPromptsInUpdateMode(t *testing.T) {
	update := []string{
		"## Updating the tree", "`.orchestra/run/spec-update.md`",
		"this session updates that tree rather than file a new one",
		"Never change a closed issue, ticket or epic",
		`bd defer <ID> --reason "Obsolete since the spec changed at commit <commit>: <what changed>"`,
		"`bd dep remove <the ticket that waits> <ID>`",
		"Leave a ticket in progress as it is",
		"filed by an earlier update that stopped part way",
		"**The root epic** is filed already, and the first message names it: file no other",
		"`bd update <root epic> --append-notes …`",
		"Nothing in the root epic's place",
		"--set-metadata spec_sha256=<sha256> --set-metadata spec_commit=<commit>",
		"carries the metadata `spec`", "never put a file name in text that many tickets share",
	}
	gone := []string{"## A project with no code yet", "bd create --type epic --priority <its most urgent", "{{",
		"the root epic first and each epic before its tickets", "Their root epic (the epic among them with no parent)"}
	for _, tc := range []struct {
		name  string
		write func(repo string) (string, error)
		want  []string
		gone  []string
	}{
		{"headless", func(repo string) (string, error) { return WriteSpecPrompt(repo, true) },
			[]string{"## Answering", "the one the first message names", "never ask one",
				"in the note you add for the change"},
			[]string{"## Questions", "--notes`, once the tree is filed"}},
		{"refinement in a pane", func(repo string) (string, error) { return WriteRefinePrompt(repo, true) },
			[]string{"## Questions", "In this update, ask only about the change", "sum up the update",
				"under the heading `Decisions`", "it closes this session by itself",
				"Once the user agrees to the update, file as you go"},
			[]string{"## Answering", "`assumptions`"}},
		{"refinement on the terminal", func(repo string) (string, error) { return WriteTerminalRefine(repo, true) },
			[]string{"## Questions", "typing `/exit` hands back to orchestra"},
			[]string{"## Answering", "it closes this session by itself"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := tc.write(runFolder(t))
			if err != nil {
				t.Fatal(err)
			}
			text := readText(t, p)
			for _, want := range append(tc.want, update...) {
				if !strings.Contains(text, want) {
					t.Errorf("the instructions lack %q", want)
				}
			}
			for _, g := range append(tc.gone, gone...) {
				if strings.Contains(text, g) {
					t.Errorf("the instructions have %q", g)
				}
			}
		})
	}
}

// A new spec's instructions have none of the update's sections, and set the root epic's SHA-256 and
// commit last of all, as the sign that its tree is filed.
func TestSpecPromptsForANewSpec(t *testing.T) {
	for name, write := range map[string]func(repo string) (string, error){
		"headless":                   func(repo string) (string, error) { return WriteSpecPrompt(repo, false) },
		"refinement in a pane":       func(repo string) (string, error) { return WriteRefinePrompt(repo, false) },
		"refinement on the terminal": func(repo string) (string, error) { return WriteTerminalRefine(repo, false) },
	} {
		p, err := write(runFolder(t))
		if err != nil {
			t.Fatal(err)
		}
		text := readText(t, p)
		for _, gone := range []string{"## Updating the tree", "spec-update.md", "In this update", "{{"} {
			if strings.Contains(text, gone) {
				t.Errorf("%s: the instructions have %q", name, gone)
			}
		}
		for _, want := range []string{`--metadata '{"spec":"<path>"}' --silent`, "Last of all, once everything is " +
			"filed and checked, set the root epic's `spec_sha256` and `spec_commit`",
			"a later run that finds them carries on with the tree as it is"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: the instructions lack %q", name, want)
			}
		}
	}
}

// WriteSpecUpdate writes what an update reads to .orchestra/run/spec-update.md, over an earlier
// update's, and returns its path in the checkout, as the breakdown's first message names it.
func TestWriteSpecUpdate(t *testing.T) {
	repo := runFolder(t)
	for _, text := range []string{"# An earlier change\n", "# How the spec changed\n"} {
		rel, err := WriteSpecUpdate(repo, []byte(text))
		if err != nil || rel != ".orchestra/run/spec-update.md" {
			t.Fatalf("WriteSpecUpdate: %q, %v", rel, err)
		}
		if b, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel))); err != nil || string(b) != text {
			t.Errorf("the file holds %q, %v; want %q", b, err, text)
		}
	}
}
