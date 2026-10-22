package dispatch

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// What a fast-forward of Base brings in before the first ticket is taken by one function
// (TakeFastForward), whichever of the run's start, the overnight pre-flight and a spec run's fetch
// fast-forwarded (orchestra-4n6j.1): each setting on its own, and the worker prompt.

// forwardFiles is git for TakeFastForward: the fast-forward changed these files, from the repository's
// top, to these texts.
type forwardFiles map[string]string

func (f forwardFiles) ChangedFiles(context.Context, string, string, string) []string {
	var files []string
	for name := range f {
		files = append(files, name)
	}
	return files
}

func (f forwardFiles) FileAt(_ context.Context, _, _, path string) (string, error) {
	text, ok := f[path]
	if !ok {
		return "", errors.New("no such file")
	}
	return text, nil
}

// eventTexts are the texts of evs.
func eventTexts(evs []Event) []string {
	var texts []string
	for _, ev := range evs {
		texts = append(texts, ev.Text)
	}
	return texts
}

// takeConfig is a run's Config before a fast-forward, its checks and setup set.
func takeConfig(t *testing.T) Config {
	repo := t.TempDir()
	return Config{Repo: repo, Base: "main", Check: "make fast", CheckFull: "make full", Setup: "make deps",
		SetupFiles: project.DefaultSetupFiles, PromptFile: filepath.Join(repo, ".orchestra", "worker-prompt.md")}
}

const takeBy = "the fast-forward to origin/main"

// A fast-forward that doesn't change settings.json, as in a repository that has none yet, keeps the
// run's check commands and setup: it doesn't blank them.
func TestAFastForwardWithoutSettingsKeepsTheChecks(t *testing.T) {
	t.Parallel()
	c := takeConfig(t)
	prompt := "Work on TICKET_ID."
	lines := TakeFastForward(t.Context(), forwardFiles{"main.go": "package main"}, &c, &prompt, takeBy, "a", "b")
	if len(lines) != 0 {
		t.Errorf("lines %q, want none", eventTexts(lines))
	}
	if c.Check != "make fast" || c.CheckFull != "make full" || c.Setup != "make deps" {
		t.Errorf("check_fast %q, check_full %q, setup %q: want those the run had", c.Check, c.CheckFull, c.Setup)
	}
}

// A requirement the run would refuse leaves the requirements as they were, and the setup and the
// checks are taken all the same.
func TestAFastForwardTakesEachSettingOnItsOwn(t *testing.T) {
	t.Parallel()
	c := takeConfig(t)
	var prompt string
	g := forwardFiles{project.SettingsFile: `{"setup": "npm ci", "check_fast": "npm test",
		"requires": [{"name": "jq"}]}`}
	lines := TakeFastForward(t.Context(), g, &c, &prompt, takeBy, "a", "b")
	if c.Setup != "npm ci" || c.Check != "npm test" || c.CheckFull != "" || len(c.Requires) != 0 {
		t.Errorf("setup %q, check_fast %q, check_full %q, requires %+v: want the setup and the checks taken, "+
			"the requirements kept", c.Setup, c.Check, c.CheckFull, c.Requires)
	}
	got := eventTexts(lines)
	for _, want := range []string{
		"  SETUP: " + takeBy + " changed .orchestra/settings.json on main: the setup is 'npm ci' from now on",
		"  SETTINGS_UNREAD: " + takeBy + " changed the requirements in .orchestra/settings.json on main, but ",
		"  CHECKS: " + takeBy + " changed .orchestra/settings.json on main: the merge check (check_fast) is " +
			"'npm test', the full check (check_full) is none from now on",
	} {
		if !slices.ContainsFunc(got, func(l string) bool { return strings.HasPrefix(l, want) }) {
			t.Errorf("lines lack %q:\n%q", want, got)
		}
	}
}

// A settings.json that doesn't parse leaves every setting as it was, with one warning.
func TestAFastForwardKeepsTheSettingsItCannotRead(t *testing.T) {
	t.Parallel()
	c := takeConfig(t)
	var prompt string
	lines := TakeFastForward(t.Context(), forwardFiles{project.SettingsFile: `{"setup": `}, &c, &prompt, takeBy,
		"a", "b")
	if c.Setup != "make deps" || c.Check != "make fast" {
		t.Errorf("setup %q, check_fast %q: want those the run had", c.Setup, c.Check)
	}
	if got := eventTexts(lines); len(got) != 1 || lines[0].Kind != EvWarn ||
		!strings.HasPrefix(got[0], "  SETTINGS_UNREAD: "+takeBy+" changed .orchestra/settings.json on main, but it "+
			"could not be read: ") {
		t.Errorf("lines %q, want one SETTINGS_UNREAD", got)
	}
}

// A worker prompt the fast-forward changed is the run's, and said.
func TestAFastForwardTakesTheWorkerPrompt(t *testing.T) {
	t.Parallel()
	c := takeConfig(t)
	prompt := "Work on TICKET_ID."
	g := forwardFiles{".orchestra/worker-prompt.md": "Work on TICKET_ID, as upstream says."}
	lines := TakeFastForward(t.Context(), g, &c, &prompt, takeBy, "a", "b")
	if prompt != "Work on TICKET_ID, as upstream says." {
		t.Errorf("the prompt is %q, want the one the fast-forward brought in", prompt)
	}
	want := []string{"  PROMPT: " + takeBy + " changed .orchestra/worker-prompt.md on main: the run's workers " +
		"get it as committed"}
	if got := eventTexts(lines); !slices.Equal(got, want) {
		t.Errorf("lines %q, want %q", got, want)
	}
}
