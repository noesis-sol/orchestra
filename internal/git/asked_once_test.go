package git

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// What orchestra asks git once where it used to ask two or three times: the main checkout's state and
// branch from one git status, a finished branch's commits from one git log, and where a checkout is
// from one rev-parse, remembered for the command.

func TestDirtyTreeAndBranchIsDirtyTreeAndTheBranch(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	if dirty, br, err := g.DirtyTreeAndBranch(ctx, repo); dirty != "" || br != "main" || err != nil {
		t.Errorf("before the first commit: got %q, %q, %v", dirty, br, err)
	}
	git("commit", "-q", "--allow-empty", "-m", "Start")
	write(t, repo, ".orchestra/run/notes.txt", "scratch\n")
	if dirty, br, err := g.DirtyTreeAndBranch(ctx, repo); dirty != "" || br != "main" || err != nil {
		t.Errorf("clean but for .orchestra/: got %q, %q, %v", dirty, br, err)
	}
	write(t, repo, "a.txt", "a\n")
	want, err := g.DirtyTree(ctx, repo)
	if err != nil || want == "" {
		t.Fatalf("DirtyTree: %q, %v", want, err)
	}
	if dirty, br, err := g.DirtyTreeAndBranch(ctx, repo); dirty != want || br != "main" || err != nil {
		t.Errorf("a.txt untracked: got %q, %q, %v; want %q", dirty, br, err, want)
	}

	// An upstream adds to the branch line; a tag of the branch's name, to symbolic-ref's name for it.
	git("add", "a.txt")
	git("commit", "-q", "-m", "a")
	git("branch", "-q", "before-a", "HEAD~1")
	git("branch", "-q", "--set-upstream-to=before-a")
	git("tag", "main")
	if _, br, err := g.DirtyTreeAndBranch(ctx, repo); br != "main" || err != nil {
		t.Errorf("ahead of its upstream: got %q, %v", br, err)
	}
	git("switch", "-q", "--detach")
	if _, br, err := g.DirtyTreeAndBranch(ctx, repo); br != "" || err != nil {
		t.Errorf("detached: got %q, %v", br, err)
	}
	if dirty, br, err := g.DirtyTreeAndBranch(ctx, filepath.Join(repo, "gone")); err == nil {
		t.Errorf("a missing folder: got %q, %q and no error", dirty, br)
	}
}

func TestBranchOfStatus(t *testing.T) {
	for _, tc := range []struct {
		line, want string
		ok         bool
	}{
		{"## main", "main", true},
		{"## wt/orchestra-1.2", "wt/orchestra-1.2", true},
		{"## main...origin/main", "main", true},
		{"## main...origin/main [different]", "main", true},
		{"## main...origin/main [gone]", "main", true},
		{"## No commits yet on trunk", "trunk", true},
		{"## Initial commit on trunk", "trunk", true}, // git before 2.17
		{"## HEAD (no branch)", "", true},
		{"?? a.txt", "", false},
		{"", "", false},
	} {
		if got, ok := branchOfStatus(tc.line); got != tc.want || ok != tc.ok {
			t.Errorf("%q: got %q, %v; want %q, %v", tc.line, got, ok, tc.want, tc.ok)
		}
	}
}

func TestOwnCommitsIsCommitNamingAndCountCommits(t *testing.T) {
	ctx, g := context.Background(), Git{}
	repo, git := newRepo(t)
	git("commit", "-q", "--allow-empty", "-m", "Start")
	git("switch", "-q", "-c", "wt/k-1")
	for _, msg := range []string{"k-1: first", "k-10: not this ticket", "k-1: second", "a commit naming none"} {
		git("commit", "-q", "--allow-empty", "-m", msg)
	}
	for _, tc := range []struct{ base, ticket string }{
		{"main", "k-1"}, {"main", "k-2"}, {"wt/k-1", "k-1"}, {"wt/k-1~2", "k-1"},
	} {
		naming, own := g.OwnCommits(ctx, repo, tc.base, "wt/k-1", tc.ticket)
		wantNaming := g.CommitNaming(ctx, repo, tc.base, "wt/k-1", tc.ticket)
		wantOwn := g.CountCommits(ctx, repo, tc.base+"..wt/k-1")
		if naming != wantNaming || own != wantOwn {
			t.Errorf("%s..wt/k-1, %s: got %q, %d; want %q, %d", tc.base, tc.ticket, naming, own, wantNaming, wantOwn)
		}
	}
	if naming, own := g.OwnCommits(ctx, repo, "main", "wt/k-1", "k-1"); own != 4 || naming == "" {
		t.Errorf("got %q, %d; want k-1: second, 4", naming, own)
	}
	if naming, own := g.OwnCommits(ctx, repo, "main", "no-such-branch", "k-1"); naming != "" || own != -1 {
		t.Errorf("a missing branch: got %q, %d; want \"\", -1", naming, own)
	}
}

// A context made by Remember answers again from memory, for the folder asked about and for its
// checkout's top folder; a failure is asked again.
func TestRememberAsksGitOncePerCheckout(t *testing.T) {
	g := Git{}
	repo, git := newRepo(t)
	git("commit", "-q", "--allow-empty", "-m", "Start")
	sub := filepath.Join(repo, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := Remember(context.Background())
	top, err := g.TopLevel(ctx, sub)
	if err != nil {
		t.Fatal(err)
	}
	common, err := g.CommonDir(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}

	// Without its .git, the checkout can be told only from memory.
	if err := os.Rename(filepath.Join(repo, ".git"), filepath.Join(repo, "moved")); err != nil {
		t.Fatal(err)
	}
	if got, err := g.TopLevel(ctx, sub); got != top || err != nil {
		t.Errorf("TopLevel(sub) again: got %q, %v; want %q", got, err, top)
	}
	if got, err := g.CommonDir(ctx, top); got != common || err != nil {
		t.Errorf("CommonDir(top): got %q, %v; want %q", got, err, common)
	}
	if linked, err := g.LinkedWorktree(ctx, top); linked || err != nil {
		t.Errorf("LinkedWorktree(top): got %v, %v; want false", linked, err)
	}
	if got, err := g.CommonDir(context.Background(), top); err == nil {
		t.Errorf("without Remember: got %q and no error", got)
	}

	// Not a repository yet, then one: its failure isn't remembered.
	if err := os.Rename(filepath.Join(repo, "moved"), filepath.Join(repo, ".git")); err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	if got, err := g.TopLevel(ctx, other); err == nil {
		t.Fatalf("not a repository: got %q and no error", got)
	}
	gitIn(t, other, "init", "-q", "-b", "main")
	if got, err := g.TopLevel(ctx, other); err != nil || got == "" {
		t.Errorf("once git init made it: got %q, %v", got, err)
	}
}

// A bare repository has no top level: its common directory is itself, and it is no linked worktree.
func TestLocationsOfABareRepository(t *testing.T) {
	g := Git{}
	bare := t.TempDir()
	gitIn(t, bare, "init", "-q", "--bare")
	ctx := Remember(context.Background())
	if got, err := g.TopLevel(ctx, bare); err == nil {
		t.Errorf("TopLevel: got %q and no error", got)
	}
	want, err := filepath.EvalSymlinks(bare) // macOS's TempDir is under a symlink
	if err != nil {
		t.Fatal(err)
	}
	if got, err := g.CommonDir(ctx, bare); err != nil || got != want {
		t.Errorf("CommonDir: got %q, %v; want %q", got, err, want)
	}
	if linked, err := g.LinkedWorktree(ctx, bare); linked || err != nil {
		t.Errorf("LinkedWorktree: got %v, %v; want false", linked, err)
	}
}

// gitIn runs git in dir, failing the test when it fails.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	if out, err := command.Output(context.Background(), 0, dir, "git", args...); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
