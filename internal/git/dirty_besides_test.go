package git

import (
	"context"
	"testing"
)

func TestDirtyTreeAndBranchBesidesLeavesThePathsOut(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	git("commit", "-q", "--allow-empty", "-m", "Start")
	write(t, repo, "docs/app.md", "# An app\n")
	if dirty, br, err := g.DirtyTreeAndBranchBesides(ctx, repo, "docs/app.md"); dirty != "" || br != "main" || err != nil {
		t.Errorf("only the spec untracked: got %q, %q, %v", dirty, br, err)
	}
	write(t, repo, "a.txt", "a\n")
	want, err := g.DirtyTreeBesides(ctx, repo, "docs/app.md")
	if err != nil || want == "" {
		t.Fatalf("DirtyTreeBesides: %q, %v", want, err)
	}
	if dirty, br, err := g.DirtyTreeAndBranchBesides(ctx, repo, "docs/app.md"); dirty != want || br != "main" || err != nil {
		t.Errorf("a.txt untracked too: got %q, %q, %v; want %q", dirty, br, err, want)
	}
}
