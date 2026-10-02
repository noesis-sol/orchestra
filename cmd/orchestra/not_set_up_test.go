package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// In a repository orchestra init never ran in, a run and a --feature request say to run it first,
// in place of the missing worker prompt and Beads database it would fix, and exit 2.
func TestRunSaysToRunInitFirst(t *testing.T) {
	repo, git := gitRepo(t)
	env := map[string]string{"HERDR_ENV": "1", "HERDR_WORKSPACE_ID": "w7Q"}
	for _, args := range [][]string{nil, {"--feature", "add a widget"}} {
		_, stderr, err := runIn(t, repo, env, args...)
		if exitOf(err) != 2 || stderr != notSetUpMessage+"\n" {
			t.Errorf("%q: exit %d, stderr:\n%s", args, exitOf(err), stderr)
		}
	}

	// What init doesn't fix is still listed, after it.
	git(repo, "checkout", "-q", "--detach")
	_, stderr, err := runIn(t, repo, nil)
	if exitOf(err) != 2 || !strings.HasPrefix(stderr, notSetUpMessage+"\n\nAlso fix before a run:\n  - ") {
		t.Errorf("exit %d, stderr:\n%s", exitOf(err), stderr)
	}
	for _, want := range []string{"Not running inside a Herdr pane", "detached HEAD"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
	for _, unwanted := range []string{"Worker prompt", "Beads database", "cannot start"} {
		if strings.Contains(stderr, unwanted) {
			t.Errorf("stderr has %q:\n%s", unwanted, stderr)
		}
	}

	// Beads alone isn't orchestra set up.
	if err := os.Mkdir(filepath.Join(repo, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	git(repo, "checkout", "-q", "-")
	if _, stderr, err := runIn(t, repo, env); exitOf(err) != 2 || stderr != notSetUpMessage+"\n" {
		t.Errorf("with .beads/: exit %d, stderr:\n%s", exitOf(err), stderr)
	}
}

// A project with settings but no worker prompt is set up, partly: the prompt is named as in the
// repository, and orchestra init recreates it.
func TestRunNamesAPartlySetUpProjectsMissingPrompt(t *testing.T) {
	repo := configFixture(t, "{}")
	if err := os.Remove(filepath.Join(repo, ".orchestra", "worker-prompt.md")); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HERDR_ENV": "1", "HERDR_WORKSPACE_ID": "w7Q"}
	const want = "orchestra cannot start:\n" +
		"  - Worker prompt not found: .orchestra/worker-prompt.md. Recreate it with: orchestra init\n"
	if _, stderr, err := runIn(t, repo, env); exitOf(err) != 2 || stderr != want {
		t.Errorf("exit %d, stderr:\n%s", exitOf(err), stderr)
	}
}

// A project set up before orchestra init, with its prompt in .claude/, runs as before.
func TestConfigLegacyProjectIsSetUp(t *testing.T) {
	repo := configFixture(t, "")
	if err := os.RemoveAll(filepath.Join(repo, ".orchestra")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".claude", "worker-prompt.md"), []byte("Work on TICKET_ID."), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, p := loadWith(t); c.notSetUp || len(p) > 0 {
		t.Errorf("legacy: not set up %v, problems %v", c.notSetUp, p)
	}
}

// A worker prompt of the run's own (-prompt, WORKER_PROMPT) needs no orchestra init; one that's
// missing is named as given, without suggesting init, which wouldn't write it.
func TestConfigOwnPromptNeedsNoInit(t *testing.T) {
	repo := configFixture(t, "")
	if err := os.RemoveAll(filepath.Join(repo, ".orchestra")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "prompt.md"), []byte("Work on TICKET_ID."), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, p := loadWith(t, "-prompt", "prompt.md"); c.notSetUp || len(p) > 0 {
		t.Errorf("own prompt: not set up %v, problems %v", c.notSetUp, p)
	}
	t.Setenv("WORKER_PROMPT", "missing.md")
	const want = "Worker prompt not found: missing.md (from -prompt or WORKER_PROMPT)."
	if c, p := loadWith(t); c.notSetUp || len(p) != 1 || p[0] != want {
		t.Errorf("missing own prompt: not set up %v, problems %q", c.notSetUp, p)
	}
}
