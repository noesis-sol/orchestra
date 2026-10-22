package git

import (
	"context"
	"testing"
)

// What changed between two commits, and the commit that last changed a file, say none without an
// error, and git failing to say, as for a rev naming no commit or outside a repository, is an error:
// the setup, the settings, the worker prompt and the footprints decide by them (orchestra-4n6j.2).
func TestChangedFilesAndLastCommitTellNoneFromAFailure(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	write(t, repo, "a.md", "a\n")
	git("add", ".")
	git("commit", "-q", "-m", "Start")
	first := answered[string](t)(g.Head(ctx, repo, "HEAD"))
	git("branch", "wt/x")
	notRepo := t.TempDir()

	if files, err := g.ChangedFiles(ctx, repo, first, "wt/x"); len(files) != 0 || err != nil {
		t.Errorf("ChangedFiles, nothing changed = %q, %v; want none, no error", files, err)
	}
	if files, err := g.ChangedFiles(ctx, repo, first, "no-such-branch"); files != nil || err == nil {
		t.Errorf("ChangedFiles to no commit = %q, %v; want git's error", files, err)
	}
	if files, err := g.ChangedFiles(ctx, repo, "", "no-such-branch"); files != nil || err == nil {
		t.Errorf("ChangedFiles of no first commit = %q, %v; want git's error", files, err)
	}

	if commit, err := g.LastCommit(ctx, repo, "a.md"); commit != first || err != nil {
		t.Errorf("LastCommit of a.md = %q, %v; want %q", commit, err, first)
	}
	if commit, err := g.LastCommit(ctx, repo, "b.md"); commit != "" || err != nil {
		t.Errorf("LastCommit of a file no commit changed = %q, %v; want none, no error", commit, err)
	}
	if commit, err := g.LastCommit(ctx, notRepo, "a.md"); commit != "" || err == nil {
		t.Errorf("LastCommit outside a repository = %q, %v; want git's error", commit, err)
	}
}
