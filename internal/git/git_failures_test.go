package git

import (
	"context"
	"path/filepath"
	"testing"
)

// answered returns a function that gives back what git answered, failing t when git failed to answer.
func answered[T any](t *testing.T) func(T, error) T {
	return func(v T, err error) T {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
}

// Every question git answers with nothing, no or none says so without an error, and git failing to
// answer, as outside a repository or for a rev naming no commit, is an error: a caller can't take a
// failure for the answer.
func TestGitQuestionsTellNothingFromAFailure(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	if head, err := g.Head(ctx, repo, "HEAD"); head != "" || err != nil {
		t.Errorf("Head before the first commit = %q, %v; want none, no error", head, err)
	}
	write(t, repo, "a.md", "a\n")
	git("add", ".")
	git("commit", "-q", "-m", "Start")
	git("branch", "wt/x")
	git("switch", "-q", "--orphan", "lone")
	git("commit", "-q", "--allow-empty", "-m", "Unrelated")
	git("switch", "-q", "main")
	notRepo := t.TempDir()
	gone := filepath.Join(repo, "gone")

	for _, c := range []struct {
		name    string
		ask     func() (any, error)
		want    any
		wantErr bool
	}{
		{"Head of no rev", func() (any, error) { return g.Head(ctx, repo, "no-such-branch") }, "", false},
		{"Head outside a repository", func() (any, error) { return g.Head(ctx, notRepo, "HEAD") }, "", true},
		{"MergeBase of unrelated branches", func() (any, error) { return g.MergeBase(ctx, repo, "main", "lone") },
			"", false},
		{"MergeBase with no commit", func() (any, error) { return g.MergeBase(ctx, repo, "main", "no-such-branch") },
			"", true},
		{"CountCommits with no commit", func() (any, error) { return g.CountCommits(ctx, repo, "main..no-such") },
			0, true},
		{"Unchanged, nothing changed", func() (any, error) { return g.Unchanged(ctx, repo, "main", "wt/x") }, true, false},
		{"Unchanged with no commit", func() (any, error) { return g.Unchanged(ctx, repo, "main", "no-such-branch") },
			false, true},
		{"CommitNaming with no commit", func() (any, error) {
			return g.CommitNaming(ctx, repo, "main", "no-such-branch", "x")
		}, "", true},
		{"CommitNamingOn with no commit", func() (any, error) { return g.CommitNamingOn(ctx, repo, "no-such", "x") },
			"", true},
		{"RebaseInProgress, none", func() (any, error) { return g.RebaseInProgress(ctx, repo) }, false, false},
		{"RebaseInProgress in a missing folder", func() (any, error) { return g.RebaseInProgress(ctx, gone) },
			false, true},
		{"DirtyWorktree, clean", func() (any, error) { return g.DirtyWorktree(ctx, repo) }, "", false},
		{"DirtyWorktree in a missing folder", func() (any, error) { return g.DirtyWorktree(ctx, gone) }, "", true},
		{"Tracks, not tracked", func() (any, error) { return g.Tracks(ctx, repo, "b.md") }, false, false},
		{"Tracks outside a repository", func() (any, error) { return g.Tracks(ctx, notRepo, "a.md") }, false, true},
	} {
		got, err := c.ask()
		if got != c.want || (err != nil) != c.wantErr {
			t.Errorf("%s: got %v, %v; want %v with an error %v", c.name, got, err, c.want, c.wantErr)
		}
	}

	if naming, own, err := g.OwnCommits(ctx, repo, "main", "no-such-branch", "x"); err == nil {
		t.Errorf("OwnCommits of a missing branch = %q, %d with no error", naming, own)
	}
	if files, err := g.Changes(ctx, gone); err == nil {
		t.Errorf("Changes in a missing folder = %q with no error", files)
	}
	if files, err := g.TrackedFiles(ctx, notRepo); err == nil {
		t.Errorf("TrackedFiles outside a repository = %q with no error", files)
	}
	if files, err := g.ConflictedFiles(ctx, gone); err == nil {
		t.Errorf("ConflictedFiles in a missing folder = %q with no error", files)
	}
}

// A rebase stopped on conflicts is in progress until it is aborted.
func TestRebaseInProgressUntilAborted(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	write(t, repo, "a.md", "a\n")
	git("add", ".")
	git("commit", "-q", "-m", "Start")
	git("switch", "-q", "-c", "wt/x")
	write(t, repo, "a.md", "x\n")
	git("commit", "-q", "-am", "x: change a.md")
	git("switch", "-q", "main")
	write(t, repo, "a.md", "main\n")
	git("commit", "-q", "-am", "Change a.md")
	git("switch", "-q", "wt/x")
	if out, err := g.Rebase(ctx, repo, "main"); err == nil {
		t.Fatalf("the rebase didn't stop on its conflict: %s", out)
	}
	if in := answered[bool](t)(g.RebaseInProgress(ctx, repo)); !in {
		t.Error("a rebase stopped on conflicts isn't in progress")
	}
	if out, err := g.AbortRebase(ctx, repo); err != nil {
		t.Fatalf("AbortRebase: %v\n%s", err, out)
	}
	if in := answered[bool](t)(g.RebaseInProgress(ctx, repo)); in {
		t.Error("an aborted rebase is still in progress")
	}
}
