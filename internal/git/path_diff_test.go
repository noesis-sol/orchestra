package git

import (
	"context"
	"strings"
	"testing"
)

// PathDiff is the diff of one file between two commits, its path taken literally, and nothing of the
// other files they change; "" where the file is the same in both, and git's error for a commit git
// doesn't have.
func TestPathDiff(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	write(t, repo, "docs/app[1].md", "# An app\n\nIt lists things.\n")
	write(t, repo, "other.md", "one\n")
	git("add", ".")
	git("commit", "-q", "-m", "Add the spec")
	first := answered[string](t)(g.Head(ctx, repo, "HEAD"))
	write(t, repo, "docs/app[1].md", "# An app\n\nIt lists things, sorted.\n")
	write(t, repo, "other.md", "two\n")
	git("commit", "-q", "-am", "Change the spec")
	second := answered[string](t)(g.Head(ctx, repo, "HEAD"))

	diff, err := g.PathDiff(ctx, repo, first, second, "docs/app[1].md")
	if err != nil || !strings.HasPrefix(diff, "diff --git a/docs/app[1].md b/docs/app[1].md\n") ||
		!strings.HasSuffix(diff, "\n-It lists things.\n+It lists things, sorted.\n") || strings.Contains(diff, "other.md") {
		t.Errorf("the diff (%v):\n%s", err, diff)
	}
	if diff, err := g.PathDiff(ctx, repo, second, second, "docs/app[1].md"); err != nil || diff != "" {
		t.Errorf("the same commit: %q, %v", diff, err)
	}
	if _, err := g.PathDiff(ctx, repo, strings.Repeat("d", 40), second, "docs/app[1].md"); err == nil ||
		!strings.Contains(err.Error(), "bad object") {
		t.Errorf("a commit git doesn't have: %v", err)
	}
}
