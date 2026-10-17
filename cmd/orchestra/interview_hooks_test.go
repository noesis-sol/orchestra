package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// In tmux, which reads claude's state from its hooks, the interview's claude reports through them to
// its own run folder; in Herdr, which reads its screen, it has none.
func TestInterviewInATmuxPaneReportsThroughHooks(t *testing.T) {
	for _, hooks := range []bool{true, false} {
		repo, _ := gitRepo(t)
		panes := &fakePanes{agent: []dispatch.AgentState{dispatch.StateIdle}}
		s := paneSession{panes: panes, pane: "%1", repo: repo, hooks: hooks, about: featureSubject{first: paneMessage}}
		if _, err := s.open(context.Background(), "/prompt.md"); err != nil {
			t.Fatal(err)
		}
		args := panes.launched
		end := slices.Index(args, "--")
		if end < 0 || end != len(args)-2 || !strings.HasPrefix(args[end+1], featureLead+" @") {
			t.Fatalf("hooks %v: claude's message isn't last, after --: %q", hooks, args)
		}
		i := slices.Index(args, "--settings")
		switch {
		case hooks && (i < 0 || i > end || !strings.HasPrefix(args[i+1],
			filepath.Join(repo, project.RunPath(project.InterviewRun))+string(filepath.Separator))):
			t.Errorf("claude's hooks don't write to %s: %q", project.InterviewRun, args)
		case !hooks && i >= 0:
			t.Errorf("claude has hooks in Herdr: %q", args)
		}
	}
}
