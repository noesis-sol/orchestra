//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// everyAnswer answers every question init asks, so that a test's init would write all it writes at once.
var everyAnswer = []string{"init", "--check", "make check", "-c", "1", "--mcp", "", "--setup", "",
	"--changelog-union", "--install-beads=false", "--verifier"}

// assertUnchanged fails the test unless init left repo as gitRepo made it: no .orchestra/, no runners,
// no .gitattributes, no Beads, nothing for git to commit, and bd never called.
func assertUnchanged(t *testing.T, repo, tools string, git func(dir string, args ...string) string) {
	t.Helper()
	for _, p := range []string{project.Dir, project.FastRunner, project.FullRunner, ".gitattributes", ".beads"} {
		if _, err := os.Stat(filepath.Join(repo, p)); !os.IsNotExist(err) {
			t.Errorf("%s: %v, want none", p, err)
		}
	}
	if status := git(repo, "status", "--porcelain"); status != "" {
		t.Errorf("git status:\n%s", status)
	}
	if b, err := os.ReadFile(filepath.Join(tools, "calls")); !os.IsNotExist(err) {
		t.Errorf("bd was called:\n%s", b)
	}
}

// init while a run holds the lock changes nothing, naming the run: what it rewrites would leave the
// run's tree dirty, and the run would take up the tickets it files.
func TestInitIsRefusedDuringARun(t *testing.T) {
	tools := fakeBeadsTools(t, true)
	repo, git := gitRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "CHANGELOG.md"), []byte("# Changelog\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(repo, "add", "CHANGELOG.md")
	git(repo, "commit", "-q", "-m", "changelog")
	started := time.Now().Truncate(time.Minute)
	holdLock(t, repo, project.Holder{PID: 44497, Started: started, Branch: "main", Pane: "w2B:p60"})

	_, stderr, err := runIn(t, repo, nil, everyAnswer...)
	want := "orchestra init: orchestra is already running in "
	held := "(pid 44497, since " + started.Format("15:04") + ", main, pane w2B:p60). init changes files"
	if exitOf(err) != dispatch.ExitSetup || !strings.Contains(stderr, want) || !strings.Contains(stderr, held) {
		t.Errorf("exit %d, stderr:\n%s\nwant it to contain %q and %q", exitOf(err), stderr, want, held)
	}
	assertUnchanged(t, repo, tools, git)
}

// init in a linked worktree, as a worker's, changes nothing there, saying what a run says: what it
// writes would land on the worktree's branch.
func TestInitIsRefusedInALinkedWorktree(t *testing.T) {
	tools := fakeBeadsTools(t, true)
	repo, git := gitRepo(t)
	wt := filepath.Join(t.TempDir(), "k-1")
	git(repo, "worktree", "add", "-q", "-b", "wt/k-1", wt)

	_, stderr, err := runIn(t, wt, nil, everyAnswer...)
	top := strings.TrimSpace(git(wt, "rev-parse", "--show-toplevel"))
	if want := "orchestra init: " + top + " is a linked worktree. Run this from the main checkout.\n"; exitOf(err) !=
		dispatch.ExitSetup || stderr != want {
		t.Errorf("exit %d, stderr:\n%s\nwant %q", exitOf(err), stderr, want)
	}
	assertUnchanged(t, wt, tools, git)
	assertUnchanged(t, repo, tools, git)
}
