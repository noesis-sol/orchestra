package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

func TestParseWorktreeOf(t *testing.T) {
	porcelain := "worktree /repo\nHEAD 111\nbranch refs/heads/main\n\n" +
		"worktree /wt/kinieta-abc\nHEAD 222\nbranch refs/heads/wt/kinieta-abc\n\n" +
		"worktree /wt/detached\nHEAD 333\ndetached\n"
	if got := parseWorktreeOf(porcelain, "wt/kinieta-abc"); got != "/wt/kinieta-abc" {
		t.Errorf("got %q", got)
	}
	if got := parseWorktreeOf(porcelain, "wt/kinieta"); got != "" {
		t.Errorf("a prefix must not match, got %q", got)
	}
}

func TestParseWorktreeOfSkipsPrunable(t *testing.T) {
	porcelain := "worktree /repo\nHEAD 111\nbranch refs/heads/main\n\n" +
		"worktree /wt/gone\nHEAD 222\nbranch refs/heads/wt/gone\nprunable gitdir file points to non-existent location\n"
	if got := parseWorktreeOf(porcelain, "wt/gone"); got != "" {
		t.Errorf("a worktree whose folder is gone must not be reused, got %q", got)
	}
}

func TestNamesIDMatchesOnlyTheWholeID(t *testing.T) {
	for _, tc := range []struct {
		msg  string
		want bool
	}{
		{"x-12: Fix the loop", true},
		{"Fix the loop (x-12).", true},
		{"wt/x-12", true},
		{"x-123: Fix the loop", false},
		{"x-12.1: Fix the child", false},
		{"x-12-a: Fix the follow-up", false},
		{"ax-12: Fix another project", false},
		{"x-12.1 and then x-12", true},
		{"x.12: Fix", false},
	} {
		if got := namesID(tc.msg, "x-12"); got != tc.want {
			t.Errorf("namesID(%q, x-12) = %v, want %v", tc.msg, got, tc.want)
		}
	}
	if namesID("x-abc1: Fix", "x-abc.1") || !namesID("x-abc.1: Fix", "x-abc.1") {
		t.Error("the . in a hierarchical ID must match only a .")
	}
}

// commit makes an empty commit with message msg in repo.
func commit(t *testing.T, repo, msg string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", repo, "commit", "--quiet", "--allow-empty", "-m", msg).CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
}

func TestCommitNamingIgnoresLongerIDs(t *testing.T) {
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "--quiet", "-b", "main"},
		{"config", "user.email", "test@example.com"},
		{"config", "user.name", "Test"},
	} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	commit(t, repo, "Start")
	if out, err := exec.Command("git", "-C", repo, "checkout", "--quiet", "-b", "wt/x-12").CombinedOutput(); err != nil {
		t.Fatalf("git checkout: %v: %s", err, out)
	}
	commit(t, repo, "x-123: Fix a follow-up")
	commit(t, repo, "x-12.1: Fix the child")
	g := Git{}
	if got := g.CommitNaming(repo, "main", "wt/x-12", "x-12"); got != "" {
		t.Errorf("commits naming x-123 and x-12.1 must not count as naming x-12, got %q", got)
	}
	if got := g.CommitNaming(repo, "main", "wt/x-12", "x-12.1"); !strings.HasSuffix(got, " x-12.1: Fix the child") {
		t.Errorf("got %q", got)
	}
	commit(t, repo, "x-12: Fix the loop\n\nAlso see x-123.")
	commit(t, repo, "Tidy up after x-12.1")
	if got := g.CommitNaming(repo, "main", "wt/x-12", "x-12"); !strings.HasSuffix(got, " x-12: Fix the loop") {
		t.Errorf("got %q, want the commit naming x-12", got)
	}
}

func TestDirtyWorktreeCountsWhatDirtyTreeLeavesOut(t *testing.T) {
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := command.Output(dir, "git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write(".claude/settings.json", "{}\n")
	git("add", ".")
	git("commit", "-q", "-m", "init")
	write(".orchestra/run/prompt.md", "Work on it.\n") // the ticket's scratch
	if d := (Git{}).DirtyWorktree(dir); d != "" {
		t.Errorf(".orchestra/run/ should not count: %q", d)
	}

	write(".claude/settings.json", "{\"model\": \"opus\"}\n")
	write(".beads/config.yaml", "prefix: t\n")
	if d := (Git{}).DirtyTree(dir); d != "" {
		t.Errorf("the main checkout's check should leave out .claude/ and .beads/: %q", d)
	}
	d := (Git{}).DirtyWorktree(dir)
	if !strings.Contains(d, ".claude/settings.json") || !strings.Contains(d, ".beads/") {
		t.Errorf("a worktree's check should count .claude/ and .beads/: %q", d)
	}
}
