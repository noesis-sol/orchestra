package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// quirkRepo makes a repository on main with an identity, and returns it with a function that runs
// git in it and returns git's output, failing the test if git fails.
func quirkRepo(t *testing.T) (string, func(args ...string) string) {
	t.Helper()
	repo := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := command.Output(context.Background(), 0, repo, "git", args...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return out
	}
	git("init", "--quiet", "-b", "main")
	git("config", "user.name", "t")
	git("config", "user.email", "t@t")
	return repo, git
}

// writeIn writes content to the file name in dir, making its folders.
func writeIn(t *testing.T, dir, name, content string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// With log.showSignature set, git log prints each signed commit's signature check on stdout: the
// commits orchestra reads must still be "<hash> <subject>".
func TestLogIgnoresShowSignature(t *testing.T) {
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("signing commits needs ssh-keygen")
	}
	repo, git := quirkRepo(t)
	key := filepath.Join(t.TempDir(), "key")
	keygen := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "t@t", "-f", key)
	if out, err := keygen.CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	writeIn(t, filepath.Dir(key), "allowed", "t@t "+string(pub))
	git("config", "gpg.format", "ssh")
	git("config", "user.signingkey", key)
	git("config", "gpg.ssh.allowedSignersFile", filepath.Join(filepath.Dir(key), "allowed"))
	git("config", "commit.gpgsign", "true")
	git("config", "log.showSignature", "true")
	git("commit", "--quiet", "--allow-empty", "-m", "Start")
	git("checkout", "--quiet", "-b", "wt/x-1")
	git("commit", "--quiet", "--allow-empty", "-m", "x-1: Fix the loop")
	if out := git("log", "-1", "--format=%s"); !strings.Contains(out, "signature") {
		t.Fatalf("git log should show the signature for this test to mean anything: %q", out)
	}

	want := strings.TrimSpace(git("rev-parse", "--short", "HEAD")) + " x-1: Fix the loop"
	g := Git{}
	ctx := context.Background()
	if got := g.CommitNaming(ctx, repo, "main", "wt/x-1", "x-1"); got != want {
		t.Errorf("CommitNaming = %q, want %q", got, want)
	}
	if got := g.CommitNamingOn(ctx, repo, "wt/x-1", "x-1"); got != want {
		t.Errorf("CommitNamingOn = %q, want %q", got, want)
	}
	if got := g.Subjects(ctx, repo, "main..wt/x-1"); got != want+"\n" {
		t.Errorf("Subjects = %q, want %q", got, want+"\n")
	}
	if got := g.OneLineLog(ctx, repo, "main..wt/x-1"); got != want+"\n" {
		t.Errorf("OneLineLog = %q, want %q", got, want+"\n")
	}
}

// A base branch or a ticket's branch named like a top-level file or folder is still read as a
// revision.
func TestRevisionsAreNotReadAsPaths(t *testing.T) {
	repo, git := quirkRepo(t)
	writeIn(t, repo, "main/README.md", "A folder named like the base branch.\n")
	writeIn(t, repo, "wt/x-2", "A file named like a ticket's branch.\n")
	writeIn(t, repo, "HEAD", "A file named HEAD.\n")
	git("add", ".")
	git("commit", "--quiet", "-m", "x-1: Add files named like revisions")
	git("checkout", "--quiet", "-b", "wt/x-2")
	git("commit", "--quiet", "--allow-empty", "-m", "x-2: Fix the loop")
	for _, rev := range []string{"main", "wt/x-2", "HEAD"} {
		if _, err := command.Output(context.Background(), 0, repo, "git", "log", "--oneline", rev); err == nil {
			t.Fatalf("git log %s should be ambiguous for this test to mean anything", rev)
		}
	}

	g := Git{}
	ctx := context.Background()
	if got := g.CommitNamingOn(ctx, repo, "main", "x-1"); !strings.HasSuffix(got, " x-1: Add files named like revisions") {
		t.Errorf("CommitNamingOn(main) = %q", got)
	}
	if got := g.CommitNamingOn(ctx, repo, "wt/x-2", "x-2"); !strings.HasSuffix(got, " x-2: Fix the loop") {
		t.Errorf("CommitNamingOn(wt/x-2) = %q", got)
	}
	if got := g.CountCommits(ctx, repo, "main"); got != 1 {
		t.Errorf("CountCommits(main) = %d, want 1", got)
	}
	if got := g.Subjects(ctx, repo, "main"); !strings.HasSuffix(got, " x-1: Add files named like revisions\n") {
		t.Errorf("Subjects(main) = %q", got)
	}
	if got := g.OneLineLog(ctx, repo, "wt/x-2"); !strings.Contains(got, " x-2: Fix the loop\n") {
		t.Errorf("OneLineLog(wt/x-2) = %q", got)
	}
	writeIn(t, repo, "HEAD", "Changed.\n")
	if got := g.DiffStat(ctx, repo); !strings.Contains(got, "HEAD |") {
		t.Errorf("DiffStat = %q, want the change to HEAD", got)
	}
	if out, err := g.ResetBranch(ctx, repo, "main"); err != nil {
		t.Fatalf("ResetBranch(main): %v\n%s", err, out)
	}
	if got, want := g.Head(ctx, repo, "wt/x-2"), g.Head(ctx, repo, "main"); got != want {
		t.Errorf("wt/x-2 is at %s after the reset, want main's %s", got, want)
	}
}

// File names with spaces or non-ASCII characters come back as they are, not split at the space or
// quoted by core.quotePath.
func TestConflictedFilesKeepsNamesExact(t *testing.T) {
	repo, git := quirkRepo(t)
	names := []string{"café.md", "docs/My Guide.md"}
	for _, n := range names {
		writeIn(t, repo, n, "Start.\n")
	}
	git("add", ".")
	git("commit", "--quiet", "-m", "Start")
	git("checkout", "--quiet", "-b", "wt/x-3")
	for _, n := range names {
		writeIn(t, repo, n, "The ticket's change.\n")
	}
	git("commit", "--quiet", "--all", "-m", "x-3: Change the docs")
	git("checkout", "--quiet", "main")
	for _, n := range names {
		writeIn(t, repo, n, "main's change.\n")
	}
	git("commit", "--quiet", "--all", "-m", "Change the docs on main")
	git("checkout", "--quiet", "wt/x-3")
	if out, err := (Git{}).Rebase(context.Background(), repo, "main"); err == nil {
		t.Fatalf("the rebase should stop on a conflict:\n%s", out)
	}

	got := (Git{}).ConflictedFiles(context.Background(), repo)
	slices.Sort(got)
	if !slices.Equal(got, names) {
		t.Errorf("ConflictedFiles = %q, want %q", got, names)
	}
}
