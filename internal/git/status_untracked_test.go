package git

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// With status.showUntrackedFiles=no, git status leaves out new files never added, and git worktree
// remove's check of a worktree too: orchestra's readers still see them, a worktree holding only those is
// dirty, and isn't removed.
func TestStatusSeesNewFilesGitIsSetToHide(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	git("commit", "-q", "--allow-empty", "-m", "Start")
	git("config", "status.showUntrackedFiles", "no")
	wt := filepath.Join(t.TempDir(), "wt")
	git("worktree", "add", "-q", "-b", "wt/x", wt)
	write(t, wt, "src/new.go", "package src\n")

	if d := answered[string](t)(g.DirtyWorktree(ctx, wt)); d != "?? src/new.go" {
		t.Errorf("DirtyWorktree: %q, want the new file", d)
	}
	if c := answered[map[string]string](t)(g.Changes(ctx, wt)); !maps.Equal(c, map[string]string{"src/new.go": "??"}) {
		t.Errorf("Changes: %q, want the new file", c)
	}
	if s := g.ShortStatus(ctx, wt); !strings.Contains(s, "?? src/") {
		t.Errorf("ShortStatus: %q, want the new folder", s)
	}
	if _, err := g.RemoveWorktree(ctx, repo, wt); err == nil {
		t.Error("RemoveWorktree removed a worktree holding a new file")
	}
	if _, err := os.Stat(filepath.Join(wt, "src/new.go")); err != nil {
		t.Errorf("the new file: %v", err)
	}

	write(t, repo, "b.txt", "b\n")
	if d := answered[string](t)(g.DirtyTree(ctx, repo)); d != "?? b.txt" {
		t.Errorf("DirtyTree: %q, want the new file", d)
	}
	if d, br, err := g.DirtyTreeAndBranch(ctx, repo); d != "?? b.txt" || br != "main" || err != nil {
		t.Errorf("DirtyTreeAndBranch: %q, %q, %v; want the new file on main", d, br, err)
	}
	if !answered[bool](t)(g.PathChanged(ctx, repo, "b.txt")) {
		t.Error("PathChanged: b.txt unchanged")
	}
}

// The readers list a path as it is, whatever core.quotePath says, a file a line.
func TestDirtyListsPathsUnquoted(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	git("commit", "-q", "--allow-empty", "-m", "Start")
	write(t, repo, "café.md", "c\n")
	write(t, repo, "a b.md", "a\n")
	if d := answered[string](t)(g.DirtyTree(ctx, repo)); d != "?? a b.md\n?? café.md" {
		t.Errorf("DirtyTree: %q", d)
	}
}

// git runs on the folder orchestra names, whatever GIT_DIR, GIT_WORK_TREE and GIT_INDEX_FILE in
// orchestra's environment say, as they do when it is run from a git hook; and asks for no credentials.
func TestGitRunsWithoutAnInheritedGitDir(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	git("commit", "-q", "--allow-empty", "-m", "Start")
	other, otherGit := newRepo(t)
	otherGit("commit", "-q", "--allow-empty", "-m", "Other")
	write(t, other, "dirty.txt", "d\n")
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	seen := filepath.Join(t.TempDir(), "prompt")
	write(t, filepath.Dir(hook), filepath.Base(hook), "#!/bin/sh\nprintf %s \"$GIT_TERMINAL_PROMPT\" > '"+seen+"'\n")
	if err := os.Chmod(hook, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(other, ".git", "index"))

	if d := answered[string](t)(g.DirtyTree(ctx, repo)); d != "" {
		t.Errorf("DirtyTree read the other repository: %q", d)
	}
	head := answered[string](t)(g.Head(ctx, repo, "HEAD"))
	write(t, repo, "a.md", "a\n")
	if _, err := g.CommitPath(ctx, repo, "a.md", "Add a"); err != nil {
		t.Fatal(err)
	}
	if now := answered[string](t)(g.Head(ctx, repo, "HEAD")); now == head {
		t.Error("CommitPath committed elsewhere")
	}
	if _, err := g.NewWorktree(ctx, repo, filepath.Join(t.TempDir(), "wt"), "wt/y", "main"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(seen); err != nil || string(got) != "0" {
		t.Errorf("the hook's GIT_TERMINAL_PROMPT: %q, %v; want 0", got, err)
	}
}
