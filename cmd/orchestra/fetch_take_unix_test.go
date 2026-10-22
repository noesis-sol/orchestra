//go:build unix

package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/tui"
)

// The overnight pre-flight's fetch and a spec run's take what their fast-forward brings in as the run's
// start does, through the one function (dispatch.TakeFastForward), and log the same lines
// (orchestra-4n6j.1).

// A spec run's fast-forward over a change to the worker prompt gives the run's workers the new one:
// the loop, the fetch done, doesn't fetch again to take it.
func TestSpecFetchTakesTheWorkerPromptItBringsIn(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	s.cfg.WorkerPrompt, s.prompt = filepath.Join(s.cfg.Repo, ".orchestra", "worker-prompt.md"), "Work on TICKET_ID."
	pushPrompt(t, git, other, "Work on TICKET_ID, as upstream now says.")
	s.fetchForSpec(runCtx(t))
	if !s.cfg.BaseFetched || s.prompt != "Work on TICKET_ID, as upstream now says." {
		t.Errorf("BaseFetched %v, prompt %q: want the fetch done, and the prompt it brought in",
			s.cfg.BaseFetched, s.prompt)
	}
}

// A fast-forward in a repository with no settings.json yet keeps the check commands and the setup the
// run has: it doesn't blank them, which would merge the night's tickets with no merge check.
func TestAFetchWithoutSettingsKeepsTheChecks(t *testing.T) {
	t.Parallel()
	for name, fetch := range map[string]func(*testing.T, *session){
		"pre-flight": func(t *testing.T, s *session) {
			wantResult(t, fetchCheck(runCtx(t), s), tui.CheckPassed, "fast-forwarded main to origin/main (1 commit)")
		},
		"spec": func(t *testing.T, s *session) { s.fetchForSpec(runCtx(t)) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, other, git := fetchSession(t)
			s.cfg.Check, s.cfg.CheckFull, s.cfg.Setup = "make fast", "make full", "make deps"
			pushFrom(git, other)
			fetch(t, s)
			if c := s.cfg; c.Check != "make fast" || c.CheckFull != "make full" || c.Setup != "make deps" {
				t.Errorf("check_fast %q, check_full %q, setup %q: want those the run had", c.Check, c.CheckFull,
					c.Setup)
			}
		})
	}
}

// Each fetch before the loop logs what its fast-forward brought in in the lines the run's start gives.
func TestEachFetchLogsWhatItTakes(t *testing.T) {
	t.Parallel()
	for name, fetch := range map[string]func(*testing.T, *session){
		"pre-flight": func(t *testing.T, s *session) { fetchCheck(runCtx(t), s) },
		"spec":       func(t *testing.T, s *session) { s.fetchForSpec(runCtx(t)) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			s, other, git := fetchSession(t)
			s.cfg.Check = "make fast"
			pushSettings(t, git, other, `{"setup": "npm ci", "check_fast": "npm test"}`)
			fetch(t, s)
			lines := s.log.RunLines()
			for _, want := range []string{
				"  SETUP: the fast-forward to origin/main changed .orchestra/settings.json on main: the setup is " +
					"'npm ci' from now on",
				"  CHECKS: the fast-forward to origin/main changed .orchestra/settings.json on main: the merge " +
					"check (check_fast) is 'npm test' from now on",
			} {
				if !slices.ContainsFunc(lines, func(l string) bool { return strings.HasSuffix(l, want) }) {
					t.Errorf("the log lacks %q:\n%s", want, strings.Join(lines, "\n"))
				}
			}
		})
	}
}
