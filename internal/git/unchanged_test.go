package git

import (
	"context"
	"testing"
)

// A branch is unchanged when its commits, taken together, change nothing since it left the base,
// whatever the base has done since; git that can't tell says it changed.
func TestUnchanged(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	write(t, repo, "a.md", "a\n")
	git("add", ".")
	git("commit", "-q", "-m", "Start")
	git("branch", "wt/x")
	if !g.Unchanged(ctx, repo, "main", "wt/x") {
		t.Error("a branch with no commits of its own should be unchanged")
	}

	git("switch", "-q", "wt/x")
	write(t, repo, "a.md", "x\n")
	git("commit", "-q", "-am", "x: change a.md")
	if g.Unchanged(ctx, repo, "main", "wt/x") {
		t.Error("a branch with a change should not be unchanged")
	}
	git("revert", "--no-edit", "HEAD")
	git("switch", "-q", "main")
	write(t, repo, "b.md", "b\n")
	git("add", ".")
	git("commit", "-q", "-m", "Add b.md")
	if !g.Unchanged(ctx, repo, "main", "wt/x") {
		t.Error("a commit and its revert should leave the branch unchanged, though main moved on")
	}
	if g.Unchanged(ctx, repo, "main", "no-such-branch") {
		t.Error("a branch git can't find should not be unchanged")
	}
}
