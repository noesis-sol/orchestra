//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// A spec run brings the base branch up to its upstream before it commits the spec, so that the spec's
// commit doesn't leave the base diverged from an upstream that moved.

// withUpstream has main in repo follow a new bare remote, and pushes n commits to it from a clone,
// returning the last.
func withUpstream(t *testing.T, repo string, n int) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "remote.git")
	gitIn(t, repo, "init", "-q", "--bare", "-b", "main", bare)
	gitIn(t, repo, "remote", "add", "origin", bare)
	gitIn(t, repo, "push", "-q", "-u", "origin", "main")
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, repo, "clone", "-q", bare, other)
	for range n {
		gitIn(t, other, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "Merge #1")
	}
	gitIn(t, other, "push", "-q", "origin", "main")
	return gitIn(t, other, "rev-parse", "HEAD")
}

func TestSpecRunFetchesTheBaseBeforeItCommitsTheSpec(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	for _, tc := range []struct {
		name   string
		inside bool // the spec untracked in the repository, rather than outside it
	}{{"outside", false}, {"inside", true}} {
		t.Run(tc.name, func(t *testing.T) {
			repo, spec, _ := specRunFixture(t)
			useBreakdown(t, &fakeBreakdown{repo: repo, root: "s-1"})
			if tc.inside {
				spec = writeSpec(t, filepath.Join(repo, "docs", "app.md"), "# An app\n")
			}
			pushed := withUpstream(t, repo, 3)

			stdout, stderr, code := runSpecIn(t, repo, leaveWorker(t, "s-1.2"), "--spec", spec, "--plain")
			if code != 0 {
				t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
			}
			if subject := gitIn(t, repo, "log", "-1", "--format=%s"); !strings.HasPrefix(subject, "orchestra: add spec") {
				t.Errorf("HEAD is %q, not the spec's commit", subject)
			}
			if parent := gitIn(t, repo, "rev-parse", "HEAD~1"); parent != pushed {
				t.Errorf("the spec was committed onto %s, not the upstream's %s", parent, pushed)
			}
			const ff = "BASE: fast-forwarded main to origin/main (3 commits)\n"
			if !strings.Contains(stdout, ff) {
				t.Errorf("stdout lacks %q:\n%s", ff, stdout)
			}
			log := unstamped(read(t, filepath.Join(repo, project.Dir, "orchestra.log")))
			if !strings.Contains(log, ff) {
				t.Errorf("the log lacks %q:\n%s", ff, log)
			}
			if i, j := strings.Index(log, ff), strings.Index(log, "SPEC: committed"); i < 0 || j < i {
				t.Errorf("the spec was committed before main was fast-forwarded:\n%s", log)
			}
			for name, s := range map[string]string{"stdout": stdout, "stderr": stderr, "the log": log} {
				if strings.Contains(s, "BASE_") {
					t.Errorf("%s warns of the base:\n%s", name, s)
				}
			}
		})
	}
}

// With --no-fetch, a spec run commits its spec onto the base as it is.
func TestSpecRunWithNoFetchCommitsOntoTheBaseAsItIs(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	repo, spec, _ := specRunFixture(t)
	useBreakdown(t, &fakeBreakdown{repo: repo, root: "s-1"})
	before := gitIn(t, repo, "rev-parse", "main")
	withUpstream(t, repo, 1) // origin/main is main as it is, until fetched

	stdout, stderr, code := runSpecIn(t, repo, leaveWorker(t, "s-1.2"), "--spec", spec, "--plain", "--no-fetch")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	if parent := gitIn(t, repo, "rev-parse", "HEAD~1"); parent != before {
		t.Errorf("the spec was committed onto %s, not main as it was, %s", parent, before)
	}
	if got := gitIn(t, repo, "rev-parse", "origin/main"); got != before {
		t.Errorf("--no-fetch fetched: origin/main is at %s, was %s", got, before)
	}
	if strings.Contains(stdout, "BASE") || strings.Contains(stderr, "BASE") {
		t.Errorf("--no-fetch said something of the base:\nstdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
}

// A main checkout with something uncommitted besides the spec stops a spec run before it commits
// anything, its base not fast-forwarded.
func TestSpecRunWithAStrayFileFetchesNothing(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	repo, spec, _ := specRunFixture(t)
	useBreakdown(t, &fakeBreakdown{repo: repo, root: "s-1"})
	before := gitIn(t, repo, "rev-parse", "main")
	withUpstream(t, repo, 1)
	if err := os.WriteFile(filepath.Join(repo, "stray.txt"), []byte("not mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, stderr, code := runSpecIn(t, repo, func(string) {}, "--spec", spec, "--plain")
	if code != 5 || !strings.Contains(stderr, "orchestra cannot start: uncommitted changes in") {
		t.Errorf("a stray file: exit %d, stderr:\n%s", code, stderr)
	}
	if got := gitIn(t, repo, "rev-parse", "main"); got != before {
		t.Errorf("main moved to %s from %s with a stray file", got, before)
	}
}

// The overnight pre-flight's fetch, before the spec run commits its spec, leaves the spec out of the
// main checkout's uncommitted changes, as the commit does.
func TestFetchCheckFastForwardsBesidesAnUncommittedSpec(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	pushed := pushFrom(git, other)
	s.cfg.Spec = "docs/app.md"
	writeSpec(t, filepath.Join(s.cfg.Repo, "docs", "app.md"), "# An app\n")
	wantResult(t, fetchCheck(runCtx(t), s), tui.CheckPassed, "fast-forwarded main to origin/main (1 commit)")
	if got := strings.TrimSpace(git(s.cfg.Repo, "rev-parse", "main")); got != pushed {
		t.Errorf("main is at %s, want the upstream's %s", got, pushed)
	}
}

// A spec run's fast-forward has the run take the setup it brought in, as the loop's own fetch would.
func TestSpecFetchTakesTheSettingsItBringsIn(t *testing.T) {
	t.Parallel()
	s, other, git := fetchSession(t)
	s.cfg.Setup = "make old"
	pushSettings(t, git, other, `{"setup": "npm ci"}`)
	s.fetchForSpec(runCtx(t))
	if !s.cfg.BaseFetched || s.cfg.Setup != "npm ci" {
		t.Errorf("BaseFetched %v, setup %q: want the fetch done, and the setup it brought in",
			s.cfg.BaseFetched, s.cfg.Setup)
	}
}
