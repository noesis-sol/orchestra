package git

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// In a repository with no commit yet, CommitPaths makes the first, on the branch HEAD names, with
// the files it is given, each taken literally, and nothing else: a file staged besides stays staged,
// one untracked stays untracked.
func TestCommitPathsMakesTheFirstCommit(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	git("symbolic-ref", "HEAD", "refs/heads/trunk")
	for _, f := range []string{".orchestra/settings.json", "scripts/check fast.sh", "spec[1].md", "spec1.md",
		"staged.txt", "stray.txt"} {
		write(t, repo, f, f+"\n")
	}
	git("add", "staged.txt")
	commit, err := g.CommitPaths(ctx, repo, "orchestra: set up", ".orchestra/settings.json", "scripts/check fast.sh",
		"spec[1].md")
	if err != nil {
		t.Fatal(err)
	}
	if head := g.Head(ctx, repo, "refs/heads/trunk"); head != commit || len(commit) != 40 {
		t.Errorf("trunk is at %q, the commit %q", head, commit)
	}
	if got := g.ChangedFiles(ctx, repo, "", commit); !slices.Equal(got,
		[]string{".orchestra/settings.json", "scripts/check fast.sh", "spec[1].md"}) {
		t.Errorf("the commit holds %q", got)
	}
	if staged, err := g.StagedFiles(ctx, repo); err != nil || !slices.Equal(staged, []string{"staged.txt"}) {
		t.Errorf("staged afterwards: %q (%v)", staged, err)
	}
	out, err := command.Output(ctx, 0, repo, "git", "log", "--format=%s")
	if err != nil || strings.TrimSpace(out) != "orchestra: set up" {
		t.Errorf("the log: %q (%v)", out, err)
	}
}
