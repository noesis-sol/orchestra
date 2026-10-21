//go:build unix

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// The run reads the worker prompt as it opens, before its pre-flight: the templates fix has it read
// the prompt it commits again, which runLoop then gives the loop (orchestra-qw5v.7).

func TestTemplatesFixGivesTheRunTheUpdatedPrompt(t *testing.T) {
	t.Parallel()
	s := checkSession(t, nil, nil, "")
	s.cfg.Repo = olderPromptRepo(t)
	s.cfg.WorkerPrompt = filepath.Join(s.cfg.Repo, project.Dir, "worker-prompt.md")
	if code := s.readPrompt(); code != dispatch.ExitOK {
		t.Fatalf("readPrompt: exit %d", code)
	}
	old := s.prompt
	fixWith(t, s, templatesCheck(runCtx(t), s), "Bring them up to date (orchestra init --update) and commit that?")
	if s.prompt == old {
		t.Fatal("the run keeps the worker prompt it read before the fix")
	}
	if committed := read(t, s.cfg.WorkerPrompt); s.prompt != committed {
		t.Errorf("the run's worker prompt:\n%s\nwant the one the fix committed:\n%s", s.prompt, committed)
	}
	if !strings.Contains(s.prompt, "Leave the whole suite to orchestra") {
		t.Errorf("the run's worker prompt isn't the template's:\n%s", s.prompt)
	}
}
