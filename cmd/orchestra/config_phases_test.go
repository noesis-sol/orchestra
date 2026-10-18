package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// loadSettings reads the project's settings for every phase of loadConfig that needs them: none
// outside a repository or without a settings.json, and none, with a problem, from one it can't read.
func TestLoadSettingsPhase(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, settings string // settings "" writes no settings.json
		noRepo         bool
		terminal       string
		problem        string
	}{
		{name: "no repository", noRepo: true},
		{name: "no settings"},
		{name: "settings", settings: `{"terminal": "tmux"}`, terminal: project.TerminalTmux},
		{name: "unreadable", settings: `{"terminal": `, problem: "Unreadable settings: "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := ""
			if !tc.noRepo {
				repo = t.TempDir()
			}
			if tc.settings != "" {
				if err := os.MkdirAll(filepath.Join(repo, ".orchestra"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(project.SettingsPath(repo), []byte(tc.settings), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			s, problems := loadSettings(repo)
			if s.Terminal != tc.terminal {
				t.Errorf("terminal %q, want %q", s.Terminal, tc.terminal)
			}
			switch {
			case tc.problem == "" && len(problems) > 0:
				t.Errorf("problems %q, want none", problems)
			case tc.problem != "" && (len(problems) != 1 || !strings.HasPrefix(problems[0], tc.problem)):
				t.Errorf("problems %q, want one starting %q", problems, tc.problem)
			}
		})
	}
}

// Settings a run can't read are one problem, though the terminal and the project both go by them.
func TestConfigReportsUnreadableSettingsOnce(t *testing.T) {
	t.Parallel()
	configFixture(t, `{"terminal": `)
	_, p := loadWith(t)
	if n := len(slices.DeleteFunc(slices.Clone(p), func(s string) bool {
		return !strings.HasPrefix(s, "Unreadable settings: ")
	})); n != 1 {
		t.Errorf("%d unreadable settings problems, want 1:\n%s", n, strings.Join(p, "\n"))
	}
}

// A run and init refuse a linked worktree in the same words, init's after "orchestra init: ", a
// run's as a sentence.
func TestMainCheckoutProblem(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("real git")
	}
	repo, git := gitRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	git(repo, "worktree", "add", "-q", "-b", "other", wt)
	for _, tc := range []struct {
		name, dir, want string
	}{
		{"main checkout", repo, ""},
		{"linked worktree", wt, wt + " is a linked worktree. Run this from the main checkout."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mainCheckoutProblem(context.Background(), tc.dir); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
