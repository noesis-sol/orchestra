//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/tui"
)

// The overnight pre-flight brings the base branch up to its upstream before its checks read it, once
// for the run.

// fetchSession is the session of an overnight run in a repository whose main follows a remote, and a
// clone of that remote to push from; git runs git.
func fetchSession(t *testing.T, args ...string) (s *session, other string, git func(dir string, args ...string) string) {
	t.Helper()
	s = checkSession(t, args, nil, "")
	repo, git := gitRepo(t)
	s.cfg.Repo, s.cfg.Base = repo, "main"
	bare := filepath.Join(t.TempDir(), "remote.git")
	git(repo, "init", "-q", "--bare", "-b", "main", bare)
	git(repo, "remote", "add", "origin", bare)
	git(repo, "push", "-q", "-u", "origin", "main")
	other = filepath.Join(t.TempDir(), "other")
	git(repo, "clone", "-q", bare, other)
	return s, other, git
}

// pushFrom commits to the clone at other and pushes it, returning the commit.
func pushFrom(git func(dir string, args ...string) string, other string) string {
	git(other, "commit", "-q", "--allow-empty", "-m", "Add b.md")
	git(other, "push", "-q", "origin", "main")
	return strings.TrimSpace(git(other, "rev-parse", "HEAD"))
}

func TestFetchCheckFastForwardsTheBase(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	pushed := pushFrom(git, other)
	wantResult(t, fetchCheck(runCtx(t), s), tui.CheckPassed, "fast-forwarded main to origin/main (1 commit)")
	if got := strings.TrimSpace(git(s.cfg.Repo, "rev-parse", "main")); got != pushed {
		t.Errorf("main is at %s, want the upstream's %s, for the checks after to read", got, pushed)
	}
	if !s.cfg.BaseFetched {
		t.Error("the run would fetch again: BaseFetched isn't set")
	}
	wantResult(t, fetchCheck(runCtx(t), s), tui.CheckPassed, "main has every commit of origin/main")
}

func TestFetchCheckWarnsOfADivergedBase(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	pushFrom(git, other)
	git(s.cfg.Repo, "commit", "-q", "--allow-empty", "-m", "Add c.md")
	before := git(s.cfg.Repo, "rev-parse", "main")
	wantResult(t, fetchCheck(runCtx(t), s), tui.CheckWarned,
		"main is 1 ahead of and 1 behind origin/main; the run builds on main as it is")
	if got := git(s.cfg.Repo, "rev-parse", "main"); got != before {
		t.Errorf("main moved to %s from %s", got, before)
	}
	if !s.cfg.BaseFetched {
		t.Error("the run would fetch again: BaseFetched isn't set")
	}
}

func TestFetchCheckWithNoFetch(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t, "--no-fetch")
	pushFrom(git, other)
	before, tracked := git(s.cfg.Repo, "rev-parse", "main"), git(s.cfg.Repo, "rev-parse", "origin/main")
	wantResult(t, fetchCheck(runCtx(t), s), tui.CheckPassed, "main isn't fetched from its upstream (--no-fetch)")
	if git(s.cfg.Repo, "rev-parse", "main") != before || git(s.cfg.Repo, "rev-parse", "origin/main") != tracked {
		t.Error("--no-fetch fetched, or moved main")
	}
}

func TestFetchCheckWithoutAnUpstream(t *testing.T) {
	t.Parallel()
	s := checkSession(t, nil, nil, "")
	s.cfg.Repo, _ = gitRepo(t)
	s.cfg.Base = "main"
	wantResult(t, fetchCheck(runCtx(t), s), tui.CheckPassed, "main follows no upstream: nothing to fetch")
}

func TestFetchCheckFailsOnAnUncommittedChange(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	pushFrom(git, other)
	before := git(s.cfg.Repo, "rev-parse", "main")
	if err := os.WriteFile(filepath.Join(s.cfg.Repo, "a.md"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wantResult(t, fetchCheck(runCtx(t), s), tui.CheckFailed, "main couldn't be brought up to its upstream: "+
		"uncommitted changes in "+s.cfg.Repo+", and the run would stop before its first ticket")
	if git(s.cfg.Repo, "rev-parse", "main") != before || s.cfg.BaseFetched {
		t.Error("a checkout with an uncommitted change was fast-forwarded")
	}
}
