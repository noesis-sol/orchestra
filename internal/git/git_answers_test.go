package git

import (
	"context"
	"testing"
)

// HasBranch and IsAncestor tell no, git's exit 1, from git failing to answer: a failure is an error,
// not a no, so that a caller can't take a branch git couldn't look up for one that isn't there.
func TestYesOrNoAnswersTellNoFromAFailure(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	write(t, repo, "a.md", "a\n")
	git("add", ".")
	git("commit", "-q", "-m", "Start")
	git("branch", "wt/x")
	git("commit", "-q", "--allow-empty", "-m", "On main")
	notRepo := t.TempDir()

	for _, c := range []struct {
		name    string
		ask     func() (bool, error)
		want    bool
		wantErr bool
	}{
		{"a branch there", func() (bool, error) { return g.HasBranch(ctx, repo, "wt/x") }, true, false},
		{"a branch not there", func() (bool, error) { return g.HasBranch(ctx, repo, "wt/y") }, false, false},
		{"a branch outside a repository", func() (bool, error) { return g.HasBranch(ctx, notRepo, "wt/x") },
			false, true},
		{"an ancestor", func() (bool, error) { return g.IsAncestor(ctx, repo, "wt/x", "main") }, true, false},
		{"not an ancestor", func() (bool, error) { return g.IsAncestor(ctx, repo, "main", "wt/x") }, false, false},
		{"an ancestor of no commit", func() (bool, error) { return g.IsAncestor(ctx, repo, "wt/x", "wt/y") },
			false, true},
	} {
		got, err := c.ask()
		if got != c.want || (err != nil) != c.wantErr {
			t.Errorf("%s: got %v, %v; want %v with an error %v", c.name, got, err, c.want, c.wantErr)
		}
	}
}
