//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A spec run in a clone made before orchestra init was pushed plans to set the repository up, but
// its fetch brings in the upstream's setup: it sets nothing up, keeps the upstream's settings and
// worker prompt, commits only the spec onto the upstream's commit, and runs with those settings.
func TestSpecRunKeepsTheSetupItsFetchBringsIn(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: runs orchestra on real git with fake tools")
	}
	const settings = `{"concurrent": 3}`
	repo, spec, tools, pushed := fetchedSetupFixture(t, settings)

	stdout, stderr, code := runSpecIn(t, repo, leaveWorker(t, "s-1.2"), "--spec", spec, "--plain")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s\nstdout:\n%s", code, stderr, stdout)
	}
	if parent, subject := gitIn(t, repo, "rev-parse", "HEAD~1"), gitIn(t, repo, "log", "-1", "--format=%s"); parent != pushed ||
		subject != "orchestra: add spec app.md" {
		t.Errorf("HEAD: %q after %s, want the spec's commit after the upstream's %s", subject, parent, pushed)
	}
	if strings.Contains(read(t, filepath.Join(tools, "bd-calls")), "init|") || strings.Contains(stdout, "setting it up") {
		t.Errorf("set the repository up; stdout:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(repo, "scripts")); !os.IsNotExist(err) {
		t.Errorf("wrote runners (%v)", err)
	}
	if got := read(t, project.SettingsPath(repo)); got != settings {
		t.Errorf("settings: %s, want the upstream's %s", got, settings)
	}
	if got := read(t, filepath.Join(repo, project.Dir, "worker-prompt.md")); got != fetchedPrompt {
		t.Errorf("worker prompt: %q, want the upstream's %q", got, fetchedPrompt)
	}
	const why = "SPEC: the fetch brought in orchestra's setup: the repository is set up already\n"
	if !strings.Contains(stdout, why) || !strings.Contains(stdout, "concurrent: 3,") {
		t.Errorf("stdout lacks %q, or the run didn't take the upstream's concurrency:\n%s", why, stdout)
	}
	if log := unstamped(read(t, filepath.Join(repo, project.Dir, "orchestra.log"))); !strings.Contains(log, why) {
		t.Errorf("the log lacks %q:\n%s", why, log)
	}
}

// fetchedPrompt is the worker prompt of fetchedSetupFixture's upstream.
const fetchedPrompt = "Upstream: work on TICKET_ID.\n"

// fetchedSetupFixture is emptyRepoFixture with a commit, pushed to an upstream, and then, from another
// clone, orchestra init's setup pushed there, with these settings and fetchedPrompt: what a spec run's
// fetch brings in. pushed is the upstream's commit of the setup.
func fetchedSetupFixture(t *testing.T, settings string) (repo, spec, tools, pushed string) {
	t.Helper()
	repo, spec, tools = emptyRepoFixture(t)
	useBreakdown(t, &fakeBreakdown{repo: repo, root: "s-1"})
	ident := []string{"-c", "user.name=t", "-c", "user.email=t@t"}
	gitIn(t, repo, append(ident, "commit", "-q", "--allow-empty", "-m", "Start")...)
	bare := filepath.Join(t.TempDir(), "remote.git")
	gitIn(t, repo, "init", "-q", "--bare", "-b", "main", bare)
	gitIn(t, repo, "remote", "add", "origin", bare)
	gitIn(t, repo, "push", "-q", "-u", "origin", "main")

	// Meanwhile, orchestra init is run and pushed from another clone.
	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, repo, "clone", "-q", bare, other)
	writeSpec(t, project.SettingsPath(other), settings)
	writeSpec(t, filepath.Join(other, project.Dir, "worker-prompt.md"), fetchedPrompt)
	writeSpec(t, filepath.Join(other, ".beads", "config.yaml"), "issue-prefix: app\n")
	gitIn(t, other, "add", "-A")
	gitIn(t, other, append(ident, "commit", "-q", "-m", "orchestra init")...)
	gitIn(t, other, "push", "-q", "origin", "main")
	return repo, spec, tools, gitIn(t, other, "rev-parse", "HEAD")
}
