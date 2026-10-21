package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// olderPromptRepo is a project whose worker prompt came from the template of 2026-10-16, before workers
// checked only their own change, with a section of the project's own; settings.json records no versions.
func olderPromptRepo(t *testing.T) string {
	t.Helper()
	repo := initRepo(t)
	old, err := os.ReadFile(filepath.Join("..", "..", "internal", "project", "testdata", "templates",
		"worker-prompt-2026-10-16.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, project.Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	prompt := string(old) + "\n## Project notes\n- Use pnpm, never npm.\n"
	if err := os.WriteFile(filepath.Join(repo, project.Dir, "worker-prompt.md"), []byte(prompt), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(project.SettingsPath(repo), []byte(`{"check_fast": "scripts/check-fast.sh"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestInitUpdateRewritesTheOlderSections(t *testing.T) {
	t.Parallel()
	repo := olderPromptRepo(t)
	stdout, stderr, err := runIn(t, repo, nil, "init", "--update")
	if err != nil {
		t.Fatalf("init --update: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	flat := strings.Join(strings.Fields(stdout), " ")
	for _, want := range []string{
		"rewrote the opening, Close and Working unattended in .orchestra/worker-prompt.md from the template",
		"--- a/.orchestra/worker-prompt.md",
		"-- Check your work with `scripts/check-fast.sh`",
		"+- Leave the whole suite to orchestra",
		"Commit .orchestra/worker-prompt.md and .orchestra/settings.json.",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("init --update's output lacks %q:\n%s", want, stdout)
		}
	}
	prompt := read(t, filepath.Join(repo, project.Dir, "worker-prompt.md"))
	if !strings.Contains(prompt, "Leave the whole suite to orchestra") || !strings.Contains(prompt, "## Project notes\n") {
		t.Errorf("the prompt after init --update:\n%s", prompt)
	}
	if notes := project.TemplateNotes(repo); len(notes) != 0 {
		t.Errorf("notes after init --update: %q", notes)
	}
}

func TestInitUpdateGoesAlone(t *testing.T) {
	t.Parallel()
	repo := olderPromptRepo(t)
	before := read(t, filepath.Join(repo, project.Dir, "worker-prompt.md"))
	for _, args := range [][]string{{"--update", "-c", "2"}, {"--update", "--force"}} {
		_, stderr, err := runIn(t, repo, nil, append([]string{"init"}, args...)...)
		if exitOf(err) != dispatch.ExitSetup || !strings.Contains(stderr, "--update takes no other flag") {
			t.Errorf("init %q: exit %d, stderr:\n%s", args, exitOf(err), stderr)
		}
	}
	if after := read(t, filepath.Join(repo, project.Dir, "worker-prompt.md")); after != before {
		t.Errorf("the prompt changed:\n%s", after)
	}
	_, stderr, err := runIn(t, initRepo(t), nil, "init", "--update")
	if exitOf(err) != dispatch.ExitSetup || !strings.Contains(stderr, "run orchestra init first") {
		t.Errorf("init --update in a project not set up: exit %d, stderr:\n%s", exitOf(err), stderr)
	}
}
