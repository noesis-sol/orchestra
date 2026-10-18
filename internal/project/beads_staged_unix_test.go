//go:build unix

package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetUpBeadsLeavesBdInitAloneWithChangesStaged(t *testing.T) {
	dir := fakeBeadsTools(t, true, true)
	repo, git := gitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"src/main.go", "notes.md"} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte("mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(repo, "add", "src", "notes.md")
	head := git(repo, "rev-parse", "HEAD")

	steps, working := setUpBeads(t, repo, Choice{Install: FindBeadsInstall(context.Background(), "darwin"), InstallBeads: true})
	if got := calls(t, dir); got != "" {
		t.Errorf("bd ran with changes staged:\n%s", got)
	}
	want := Step{Kind: StepMissing, Label: "Beads", Detail: "bd init didn't run: it would commit what you have " +
		"staged (notes.md and src/) with its own files; commit it, or unstage it with git reset, " +
		"then run orchestra init again"}
	if len(steps) != 1 || !same(steps[0], want) || len(working) != 0 {
		t.Errorf("steps %+v, working %q", steps, working)
	}
	if got := git(repo, "rev-parse", "HEAD"); got != head {
		t.Errorf("HEAD moved from %s to %s", head, got)
	}
	if got := git(repo, "diff", "--cached", "--name-only"); got != "notes.md\nsrc/main.go\n" {
		t.Errorf("staged:\n%s", got)
	}

	// Unstaged, the same changes stay out of bd's commit.
	git(repo, "reset", "-q")
	steps, _ = setUpBeads(t, repo, Choice{Install: FindBeadsInstall(context.Background(), "darwin"), InstallBeads: true})
	if got := calls(t, dir); got != bdInitCall || len(steps) != 1 || steps[0].Kind != StepDone {
		t.Fatalf("calls %q, steps %+v", got, steps)
	}
	if got := git(repo, "show", "--name-only", "--format=", "HEAD"); strings.Contains(got, "notes.md") ||
		strings.Contains(got, "src/") {
		t.Errorf("bd committed the user's changes:\n%s", got)
	}
}

func TestSetUpBeadsLeavesBdInitAloneWhenGitCantTell(t *testing.T) {
	dir := fakeBeadsTools(t, true, true)
	repo := t.TempDir() // not a repository: git diff --cached fails
	steps, _ := setUpBeads(t, repo, Choice{Install: FindBeadsInstall(context.Background(), "darwin"), InstallBeads: true})
	if got := calls(t, dir); got != "" {
		t.Errorf("bd ran:\n%s", got)
	}
	if len(steps) != 1 || steps[0].Kind != StepMissing ||
		!strings.HasPrefix(steps[0].Detail, "bd init didn't run: git diff --cached failed (") ||
		!strings.HasSuffix(steps[0].Detail, "so orchestra can't tell what bd init would commit") {
		t.Errorf("steps %+v", steps)
	}
}
