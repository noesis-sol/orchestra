//go:build unix

package main

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// The overnight pre-flight's fast-forward may bring in settings the run read before it: the checks
// after it, and the run, take the setup and the requirements it brought (orchestra-qw5v.4).

// pushSettings commits text as the settings.json of the clone at other and pushes it.
func pushSettings(t *testing.T, git func(dir string, args ...string) string, other, text string) {
	t.Helper()
	file := filepath.Join(other, project.SettingsFile)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	git(other, "add", "-f", project.SettingsFile)
	git(other, "commit", "-q", "-m", "Change the settings")
	git(other, "push", "-q", "origin", "main")
}

func TestFetchCheckTakesTheSettingsItBringsIn(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	s.cfg.Setup = "make old"
	pushSettings(t, git, other, `{"setup": "npm ci", "setup_files": ["package-lock.json"],
		"requires": [{"name": "jq", "check": "command -v jq", "install": "brew install jq"}]}`)
	wantResult(t, fetchCheck(runCtx(t), s), tui.CheckPassed, "fast-forwarded main to origin/main (1 commit)")
	want := []project.Requirement{{Name: "jq", Check: "command -v jq", Install: "brew install jq"}}
	if c := s.cfg; c.Setup != "npm ci" || !slices.Equal(c.SetupFiles, []string{"package-lock.json"}) ||
		!slices.EqualFunc(c.Requires, want, project.Requirement.Equal) {
		t.Errorf("setup %q, setup_files %q, requires %+v: want those the fast-forward brought in",
			c.Setup, c.SetupFiles, c.Requires)
	}
}

func TestFetchCheckKeepsTheSettingsItCannotRead(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	s.cfg.Setup = "make old"
	pushSettings(t, git, other, `{"setup": `)
	wantResult(t, fetchCheck(runCtx(t), s), tui.CheckWarned, "fast-forwarded main to origin/main (1 commit), "+
		"but .orchestra/settings.json could not be read again: ",
		"; the run keeps the setup, the requirements and the checks it read before")
	if s.cfg.Setup != "make old" {
		t.Errorf("the setup is %q, want the one the run read before", s.cfg.Setup)
	}
}
