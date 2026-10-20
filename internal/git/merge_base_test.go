package git

import (
	"context"
	"testing"
)

// MergeBase is the commit a branch left the base from, whatever either has done since, as a full
// hash; git's error when it names no commit.
func TestMergeBase(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	write(t, repo, "a.md", "a\n")
	git("add", ".")
	git("commit", "-q", "-m", "Start")
	fork := answered[string](t)(g.Head(ctx, repo, "HEAD"))
	git("branch", "wt/x")
	if got := answered[string](t)(g.MergeBase(ctx, repo, "main", "wt/x")); got != fork {
		t.Errorf("MergeBase of a branch with no commits = %q, want %s", got, fork)
	}

	git("switch", "-q", "wt/x")
	write(t, repo, "x.md", "x\n")
	git("add", ".")
	git("commit", "-q", "-m", "x: add x.md")
	git("switch", "-q", "main")
	write(t, repo, "b.md", "b\n")
	git("add", ".")
	git("commit", "-q", "-m", "Add b.md")
	if got := answered[string](t)(g.MergeBase(ctx, repo, "main", "wt/x")); got != fork {
		t.Errorf("MergeBase once both moved on = %q, want %s", got, fork)
	}
	if got, err := g.MergeBase(ctx, repo, "main", "no-such-branch"); got != "" || err == nil {
		t.Errorf("MergeBase with a branch git can't find = %q, %v; want none and git's error", got, err)
	}
}
