package git

import (
	"context"
	"testing"
)

// Base holds a branch's changes when merging it would change nothing: after a squash merge, though
// base moved on since, but not before, nor when the branch conflicts with base; git that can't tell
// says so.
func TestHolds(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	write(t, repo, "a.md", "a\n")
	git("add", ".")
	git("commit", "-q", "-m", "Start")
	git("switch", "-q", "-c", "wt/x")
	write(t, repo, "a.md", "x\n")
	write(t, repo, "x.md", "x\n")
	git("add", ".")
	git("commit", "-q", "-m", "x: change a.md")
	write(t, repo, "x.md", "x2\n")
	git("commit", "-q", "-am", "x: change x.md")
	git("switch", "-q", "main")
	if holds, err := g.Holds(ctx, repo, "main", "wt/x"); holds || err != nil {
		t.Errorf("Holds before the merge = %v, %v; want false, nil", holds, err)
	}

	git("merge", "-q", "--squash", "wt/x")
	git("commit", "-q", "-m", "x: squashed")
	write(t, repo, "b.md", "b\n")
	git("add", ".")
	git("commit", "-q", "-m", "Add b.md")
	if holds, err := g.Holds(ctx, repo, "main", "wt/x"); !holds || err != nil {
		t.Errorf("Holds once squash-merged, though main moved on = %v, %v; want true, nil", holds, err)
	}

	write(t, repo, "a.md", "changed on main\n")
	git("commit", "-q", "-am", "Change a.md")
	git("switch", "-q", "wt/x")
	write(t, repo, "a.md", "changed on wt/x\n")
	git("commit", "-q", "-am", "x: change a.md again")
	git("switch", "-q", "main")
	if holds, err := g.Holds(ctx, repo, "main", "wt/x"); holds || err != nil {
		t.Errorf("Holds of a branch that conflicts with main = %v, %v; want false, nil", holds, err)
	}
	if holds, err := g.Holds(ctx, repo, "main", "no-such-branch"); holds || err == nil {
		t.Errorf("Holds of a branch git can't find = %v, %v; want false and git's error", holds, err)
	}
}
