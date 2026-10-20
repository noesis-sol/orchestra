package git

import (
	"context"
	"testing"
)

// FileAt is a file's content as a commit has it, whatever the worktree holds since, and git's error
// for a file the commit doesn't have.
func TestFileAt(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	write(t, repo, ".orchestra/settings.json", "{\"setup\": \"npm ci\"}\n")
	git("add", ".")
	git("commit", "-q", "-m", "Set the setup")
	first := answered[string](t)(g.Head(ctx, repo, "HEAD"))
	write(t, repo, ".orchestra/settings.json", "{}\n")

	if got, err := g.FileAt(ctx, repo, first, ".orchestra/settings.json"); err != nil || got != "{\"setup\": \"npm ci\"}\n" {
		t.Errorf("FileAt = %q, %v", got, err)
	}
	if got, err := g.FileAt(ctx, repo, first, "missing.json"); err == nil {
		t.Errorf("a file the commit doesn't have: %q, no error", got)
	}
}
