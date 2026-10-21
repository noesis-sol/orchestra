//go:build unix

package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/tui"
)

// The overnight pre-flight's fast-forward may change the worker prompt the run read as it opened: the
// run's workers get the one it brought, as their worktrees are made from it (orchestra-eur4).

// pushPrompt commits text as the worker prompt in the clone other and pushes it.
func pushPrompt(t *testing.T, git func(dir string, args ...string) string, other, text string) {
	t.Helper()
	file := filepath.Join(other, ".orchestra", "worker-prompt.md")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	git(other, "add", "-f", ".orchestra/worker-prompt.md")
	git(other, "commit", "-q", "-m", "Change the worker prompt")
	git(other, "push", "-q", "origin", "main")
}

func TestFetchCheckTakesTheWorkerPromptItBringsIn(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	s.cfg.WorkerPrompt, s.prompt = filepath.Join(s.cfg.Repo, ".orchestra", "worker-prompt.md"), "Work on TICKET_ID."
	pushPrompt(t, git, other, "Work on TICKET_ID, as upstream now says.")
	wantResult(t, fetchCheck(runCtx(t), s), tui.CheckPassed, "fast-forwarded main to origin/main (1 commit)")
	if s.prompt != "Work on TICKET_ID, as upstream now says." {
		t.Errorf("the run's prompt is %q, want the one the fast-forward brought in", s.prompt)
	}
}

func TestFetchCheckKeepsAWorkerPromptWithoutItsPlaceholder(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	s.cfg.WorkerPrompt, s.prompt = filepath.Join(s.cfg.Repo, ".orchestra", "worker-prompt.md"), "Work on TICKET_ID."
	pushPrompt(t, git, other, "Work on the ticket.")
	wantResult(t, fetchCheck(runCtx(t), s), tui.CheckWarned, "fast-forwarded main to origin/main (1 commit), "+
		"but the worker prompt could not be read again: ", "has no TICKET_ID placeholder; the run gives its "+
		"workers the one it read before")
	if s.prompt != "Work on TICKET_ID." {
		t.Errorf("the run's prompt is %q, want the one it read before", s.prompt)
	}
}

// A worker prompt outside the repository, as --prompt may give, isn't the fast-forward's to change.
func TestFetchCheckLeavesAWorkerPromptOutsideTheRepository(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	s.cfg.WorkerPrompt, s.prompt = filepath.Join(t.TempDir(), "missing.md"), "Work on TICKET_ID."
	pushPrompt(t, git, other, "Work on TICKET_ID, as upstream now says.")
	wantResult(t, fetchCheck(runCtx(t), s), tui.CheckPassed, "fast-forwarded main to origin/main (1 commit)")
	if s.prompt != "Work on TICKET_ID." {
		t.Errorf("the run's prompt is %q, want the one it read before", s.prompt)
	}
}
