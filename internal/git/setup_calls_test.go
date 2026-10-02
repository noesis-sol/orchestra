package git

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// The calls orchestra init and a run's setup make: where the checkout is, what changed, what git
// tracks and how it merges a file.

// newRepo makes a repository with no commits and returns it with a way to run git in it.
func newRepo(t *testing.T) (string, func(args ...string)) {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := command.Output(context.Background(), 0, dir, "git",
			append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	return dir, git
}

// write writes content to the file name in dir, making its folders.
func write(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHeadIsEmptyBeforeTheFirstCommit(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	if got := g.Head(ctx, repo, "HEAD"); got != "" {
		t.Errorf("before the first commit: got %q", got)
	}
	git("commit", "-q", "--allow-empty", "-m", "Start")
	if got := g.Head(ctx, repo, "HEAD"); len(got) != 40 {
		t.Errorf("after it: got %q, want a commit", got)
	}
	if got := g.Head(ctx, repo, "no-such-branch"); got != "" {
		t.Errorf("a missing branch: got %q", got)
	}
}

func TestChangesAndChangedFiles(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	write(t, repo, "a.md", "a\n")
	write(t, repo, "sub dir/b.md", "b\n")
	git("add", ".")
	git("commit", "-q", "-m", "Start")
	first := g.Head(ctx, repo, "HEAD")
	if got := g.ChangedFiles(ctx, repo, "", first); !slices.Equal(got, []string{"a.md", "sub dir/b.md"}) {
		t.Errorf("the first commit's files: %q", got)
	}

	git("mv", "a.md", "renamed.md")
	write(t, repo, "sub dir/b.md", "changed\n")
	write(t, repo, "new/c.md", "c\n")
	want := map[string]string{"renamed.md": "R ", "sub dir/b.md": " M", "new/c.md": "??"}
	if got := g.Changes(ctx, repo); !maps.Equal(got, want) {
		t.Errorf("Changes = %q, want %q", got, want)
	}
	if got := g.Changes(ctx, repo, "new"); !maps.Equal(got, map[string]string{"new/c.md": "??"}) {
		t.Errorf("Changes under new/ = %q", got)
	}

	git("add", ".")
	git("commit", "-q", "-m", "Change")
	// git sees the rename, and names the file as it is now.
	if got := g.ChangedFiles(ctx, repo, first, "HEAD"); !slices.Equal(got, []string{"new/c.md", "renamed.md", "sub dir/b.md"}) {
		t.Errorf("changed since the first commit: %q", got)
	}

	gone := filepath.Join(repo, "gone")
	if got := g.Changes(ctx, gone); len(got) != 0 {
		t.Errorf("a missing folder: %q", got)
	}
	if got := g.ChangedFiles(ctx, gone, first, "HEAD"); len(got) != 0 {
		t.Errorf("a missing folder: %q", got)
	}
}

func TestTracksAndMove(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	write(t, repo, "tracked.md", "t\n")
	write(t, repo, "untracked.md", "u\n")
	git("add", "tracked.md")
	if !g.Tracks(ctx, repo, "tracked.md") || g.Tracks(ctx, repo, "untracked.md") {
		t.Error("Tracks should tell tracked.md from untracked.md")
	}
	if out, err := g.Move(ctx, repo, "tracked.md", "sub/moved.md"); err == nil {
		t.Errorf("a move into a missing folder succeeded: %s", out)
	}
	if err := os.Mkdir(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := g.Move(ctx, repo, "tracked.md", "sub/moved.md"); err != nil {
		t.Fatalf("Move: %v\n%s", err, out)
	}
	if got := g.Changes(ctx, repo); got["sub/moved.md"] != "A " {
		t.Errorf("the move should be staged: %q", got)
	}
}

func TestAttribute(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, _ := newRepo(t)
	if got, err := g.Attribute(ctx, repo, "merge", "CHANGELOG.md"); got != "unspecified" || err != nil {
		t.Errorf("no .gitattributes: got %q, %v", got, err)
	}
	write(t, repo, ".gitattributes", "*.md merge=union\n")
	if got, err := g.Attribute(ctx, repo, "merge", "CHANGELOG.md"); got != "union" || err != nil {
		t.Errorf("*.md merge=union: got %q, %v", got, err)
	}
	if got, err := g.Attribute(ctx, repo, "merge", "notes: a.md"); got != "union" || err != nil {
		t.Errorf("a path with ': ' in it: got %q, %v", got, err)
	}
	if got, err := g.Attribute(ctx, t.TempDir(), "merge", "CHANGELOG.md"); err == nil {
		t.Errorf("not a repository: got %q and no error", got)
	}
}

func TestTopLevelAndLinkedWorktree(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	git("commit", "-q", "--allow-empty", "-m", "Start")
	if err := os.Mkdir(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	top, err := g.TopLevel(ctx, filepath.Join(repo, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	if resolved, _ := filepath.EvalSymlinks(repo); top != resolved { // macOS's TempDir is under a symlink
		t.Errorf("TopLevel = %q, want %q", top, resolved)
	}
	if got, err := g.TopLevel(ctx, t.TempDir()); err == nil {
		t.Errorf("not a repository: got %q and no error", got)
	}

	wt := filepath.Join(t.TempDir(), "wt")
	git("worktree", "add", "-q", "-b", "wt/x", wt)
	for _, c := range []struct {
		dir  string
		want bool
	}{{repo, false}, {wt, true}} {
		if linked, err := g.LinkedWorktree(ctx, c.dir); linked != c.want || err != nil {
			t.Errorf("LinkedWorktree(%s) = %v, %v; want %v", c.dir, linked, err, c.want)
		}
	}
	if linked, err := g.LinkedWorktree(ctx, t.TempDir()); err == nil {
		t.Errorf("outside a repository: linked %v, no error", linked)
	}
}
