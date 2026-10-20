package project

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runFolder is a checkout with the .orchestra/run/ a breakdown writes to.
func runFolder(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, Dir, RunName), 0o755); err != nil {
		t.Fatal(err)
	}
	return repo
}

// readText reads the file at p, its words joined by single spaces, whatever the lines' ends.
func readText(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(strings.Fields(string(b)), " ")
}

// WriteRefinePrompt writes the instructions of a breakdown with the user over the headless ones, and
// removes the spec.json an earlier refinement left, so that FiledEpic only reads this one's. They
// keep the headless rules for the tree and its filing, ask in the interview's rounds only where the
// spec leaves a decision open, put the decisions in the root epic's description, ask for a go-ahead
// before filing, and hand the root epic back in spec.json; the headless parts are gone.
func TestWriteRefinePromptWritesTheRefineSections(t *testing.T) {
	repo := runFolder(t)
	if _, err := WriteSpecPrompt(repo, false); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(repo, RunPath(SpecName))
	if err := os.WriteFile(spec, []byte(`{"epic":"old-1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := WriteRefinePrompt(repo, RefineSpec)
	if err != nil || p != filepath.Join(repo, RunPath("spec-prompt.md")) {
		t.Fatalf("WriteRefinePrompt: %q, %v", p, err)
	}
	if _, err := FiledEpic(repo, RefineSpec); !errors.Is(err, ErrNoHandBack) {
		t.Errorf("after WriteRefinePrompt: %v, want ErrNoHandBack", err)
	}
	text := readText(t, p)
	for _, want := range []string{
		"# Spec breakdown",
		"The spec is mostly settled",
		"Ask in **rounds**", "give your recommended answer", "❓ **Q1**", // the interview's rounds
		"Finding _facts_ is your job, never the user's",
		"Under the heading `Decisions`",                               // in the root epic's description
		"Ask the user to go ahead, and file nothing until they agree", // the go-ahead
		"--set-metadata spec_sha256=<sha256>",                         // the headless rules for the tree
		"carries the metadata `spec`", "label `solo` and priority 0",
		"never put a file name in text that many tickets share",
		"printf '{\"epic\":\"%s\"}\\n' '<root epic ID>' > .orchestra/run/spec.json",
		"it closes this session by itself",
		"which then ends the run and runs nothing",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the refine instructions lack %q", want)
		}
	}
	for _, gone := range []string{"never ask one", "## Answering", "`assumptions`", "The time limit",
		"under the heading `Assumptions`", "typing `/exit` hands back", "{{"} {
		if strings.Contains(text, gone) {
			t.Errorf("the refine instructions have %q", gone)
		}
	}
	if headless, _ := WriteSpecPrompt(repo, false); strings.Contains(readText(t, headless), "## Questions") {
		t.Error("the headless instructions have the refinement's Questions")
	}
}

// WriteTerminalRefine writes the refine instructions for a session on orchestra's own terminal, which
// orchestra can't close: claude tells the user to type /exit once it has handed back. It leaves
// spec.json as it is.
func TestWriteTerminalRefineSaysHowToHandBack(t *testing.T) {
	repo := runFolder(t)
	if err := os.WriteFile(filepath.Join(repo, RunPath(SpecName)), []byte(`{"epic":"s-1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := WriteTerminalRefine(repo, RefineSpec)
	if err != nil || p != filepath.Join(repo, RunPath("spec-prompt.md")) {
		t.Fatalf("WriteTerminalRefine: %q, %v", p, err)
	}
	text := readText(t, p)
	if !strings.Contains(text, "## Questions") || !strings.Contains(text, "typing `/exit` hands back to orchestra") ||
		strings.Contains(text, "it closes this session by itself") {
		t.Errorf("the instructions on the terminal:\n%s", text)
	}
	if root, err := FiledEpic(repo, RefineSpec); root != "s-1" || err != nil {
		t.Errorf("spec.json reads %q, %v", root, err)
	}
}

// FiledEpic reads a refinement's root epic from spec.json, and says what is wrong with one that names none.
func TestFiledEpicReadsARefinementsRoot(t *testing.T) {
	for _, tc := range []struct {
		file, root, err string
	}{
		{"", "", "no spec.json"},
		{`{"epic":"s-1"}` + "\n", "s-1", ""},
		{`{"epic":" s-2 "}`, "s-2", ""},
		{`{"epic":""}`, "", "names no epic"},
		{`s-1`, "", "invalid character"},
	} {
		repo := runFolder(t)
		if tc.file != "" {
			if err := os.WriteFile(filepath.Join(repo, RunPath(SpecName)), []byte(tc.file), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		root, err := FiledEpic(repo, RefineSpec)
		if root != tc.root || (tc.err == "") != (err == nil) || err != nil && !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%q: got %q, %v", tc.file, root, err)
		}
		if tc.file == "" && !errors.Is(err, ErrNoHandBack) {
			t.Errorf("no file: %v, want ErrNoHandBack", err)
		}
	}
}
