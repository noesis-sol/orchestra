package gittest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Repo's repository is on main with one empty commit, init, and each test's is its own copy.
func TestRepoIsOnMainWithOneEmptyCommit(t *testing.T) {
	t.Parallel()
	a, b := Repo(t), Repo(t)
	if a == b {
		t.Fatalf("two repositories in one folder: %s", a)
	}
	for _, repo := range []string{a, b} {
		if got := strings.TrimSpace(git(t, repo, "symbolic-ref", "--short", "HEAD")); got != "main" {
			t.Errorf("%s is on %q, want main", repo, got)
		}
		if got := git(t, repo, "log", "--format=%s %an <%ae>", "--name-only"); got != "init t <t@t>\n" {
			t.Errorf("%s's history is %q, want the one empty commit init, by t <t@t>", repo, got)
		}
		if got := git(t, repo, "status", "--porcelain"); got != "" {
			t.Errorf("%s isn't clean:\n%s", repo, got)
		}
	}
	if err := os.WriteFile(filepath.Join(a, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, a, "add", "a.txt")
	git(t, a, "commit", "-q", "-m", "a")
	if got := git(t, b, "log", "--format=%s"); got != "init\n" {
		t.Errorf("a commit in one copy shows in the other: %q", got)
	}
}

// A template's build runs once, however many tests copy its repository, and each gets what it made.
func TestTemplateBuildsOnce(t *testing.T) {
	t.Parallel()
	builds := 0
	tp := NewTemplate(func(dir string) error {
		builds++
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
			return err
		}
		git(t, dir, "add", "a.txt")
		git(t, dir, "commit", "-q", "-m", "a")
		return nil
	})
	for range 3 {
		repo := tp.Repo(t)
		if got := git(t, repo, "log", "--format=%s"); got != "a\ninit\n" {
			t.Errorf("history %q, want a on init", got)
		}
		if got := git(t, repo, "status", "--porcelain"); got != "" {
			t.Errorf("the copy isn't clean:\n%s", got)
		}
	}
	if builds != 1 {
		t.Errorf("the template was built %d times, want once", builds)
	}
}
