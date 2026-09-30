package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// configFixture is a repository set up for orchestra (.orchestra/ with a prompt, Beads) inside a
// Herdr pane, with the working directory in it. loadConfig reads the process's flags and
// environment, so each test resets them.
func configFixture(t *testing.T, settings string) string {
	t.Helper()
	repo, _ := gitRepo(t)
	os.MkdirAll(filepath.Join(repo, ".orchestra"), 0o755)
	os.MkdirAll(filepath.Join(repo, ".beads"), 0o755)
	os.WriteFile(filepath.Join(repo, ".orchestra", "worker-prompt.md"), []byte("Work on TICKET_ID."), 0o644)
	if settings != "" {
		os.WriteFile(filepath.Join(repo, ".orchestra", "settings.json"), []byte(settings), 0o644)
	}
	t.Chdir(repo)
	for _, k := range []string{"WORKER_PROMPT", "NOTIFY", "WT_ROOT", "TRIAGE", "REVIEW", "ORGAN_MODEL",
		"PROMPT_AT_LAUNCH", "LIMIT", "DONE_SO_FAR", "AGENT_KIND", "ORCHESTRA_CONCURRENT", "WORKSPACE"} {
		t.Setenv(k, "")
	}
	t.Setenv("HERDR_ENV", "1")
	t.Setenv("HERDR_WORKSPACE_ID", "w7Q")
	return repo
}

// samePath compares paths whose parent may be reached through a symlink (macOS's /var).
func samePath(a, b string) bool {
	if a == b {
		return true
	}
	real := func(p string) string {
		d, err := filepath.EvalSymlinks(filepath.Dir(p))
		if err != nil {
			return p
		}
		return filepath.Join(d, filepath.Base(p))
	}
	return real(a) == real(b)
}

// loadWith runs loadConfig with these command-line arguments and the process's environment.
func loadWith(t *testing.T, args ...string) (Config, []string) {
	t.Helper()
	c, problems, err := loadConfig(args, os.Getenv, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return c, problems
}

func TestConfigConcurrencyPrecedence(t *testing.T) {
	configFixture(t, `{"concurrent": 2}`)
	if c, p := loadWith(t); len(p) > 0 || c.Concurrency != 2 {
		t.Errorf("settings: %d %v", c.Concurrency, p)
	}
	t.Setenv("ORCHESTRA_CONCURRENT", "3")
	if c, _ := loadWith(t); c.Concurrency != 3 {
		t.Errorf("the environment overrides settings: %d", c.Concurrency)
	}
	if c, _ := loadWith(t, "-c", "4"); c.Concurrency != 4 {
		t.Errorf("-c overrides the environment: %d", c.Concurrency)
	}
	if c, _ := loadWith(t, "--concurrent", "5"); c.Concurrency != 5 {
		t.Errorf("--concurrent: %d", c.Concurrency)
	}
	if _, p := loadWith(t, "-c", "20"); len(p) != 1 || !strings.Contains(p[0], "between 1 and 16") {
		t.Errorf("out of range: %v", p)
	}
	t.Setenv("ORCHESTRA_CONCURRENT", "")
	os.Remove(".orchestra/settings.json")
	if c, _ := loadWith(t); c.Concurrency != 1 {
		t.Errorf("no settings: %d", c.Concurrency)
	}
}

func TestConfigDefaultsAndLayout(t *testing.T) {
	repo := configFixture(t, `{"check": "make check"}`)
	c, p := loadWith(t)
	if len(p) > 0 {
		t.Fatal(p)
	}
	want := map[string]string{
		"Workspace":    "w7Q",
		"Base":         "main",
		"AgentKind":    "claude",
		"Check":        "make check",
		"WorkerPrompt": filepath.Join(repo, ".orchestra", "worker-prompt.md"),
		"LogPath":      filepath.Join(repo, ".orchestra", "orchestra.log"),
		"ReportsDir":   filepath.Join(repo, ".orchestra", "reports"),
		"WTRoot":       filepath.Join(filepath.Dir(repo), filepath.Base(repo)+"-worktrees"),
	}
	got := map[string]string{"Workspace": c.Workspace, "Base": c.Base, "AgentKind": c.AgentKind, "Check": c.Check,
		"WorkerPrompt": c.WorkerPrompt, "LogPath": c.LogPath, "ReportsDir": c.ReportsDir, "WTRoot": c.WTRoot}
	for k, v := range want {
		if !samePath(got[k], v) {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if c.Limit != 40 || c.DoneSoFar != 0 || !c.Triage || !c.Review || !c.LaunchPrompt || !c.Notify {
		t.Errorf("defaults: %+v", c)
	}
	t.Setenv("LIMIT", "5")
	t.Setenv("TRIAGE", "0")
	if c, _ := loadWith(t, "--workspace", "w1A"); c.Limit != 5 || c.Triage || c.Workspace != "w1A" {
		t.Errorf("environment and flags: limit %d triage %v workspace %s", c.Limit, c.Triage, c.Workspace)
	}
	t.Setenv("WORKSPACE", "ignored")
	if c, _ := loadWith(t); c.Workspace != "w7Q" {
		t.Errorf("WORKSPACE is no longer read: %s", c.Workspace)
	}
}

func TestConfigLegacyLayout(t *testing.T) {
	repo := configFixture(t, "")
	os.RemoveAll(filepath.Join(repo, ".orchestra"))
	os.MkdirAll(filepath.Join(repo, ".claude"), 0o755)
	os.WriteFile(filepath.Join(repo, ".claude", "worker-prompt.md"), []byte("Work on TICKET_ID."), 0o644)
	c, p := loadWith(t)
	if len(p) > 0 || !strings.HasSuffix(c.LogPath, ".claude/orchestrate.log") || !strings.HasSuffix(c.ReportsDir, ".claude/orchestrate-reports") {
		t.Errorf("legacy project.Layout: %s %s %v", c.LogPath, c.ReportsDir, p)
	}
}

// Every setup problem is reported, together; main exits with code 2 when there are any.
func TestConfigSetupProblems(t *testing.T) {
	repo := configFixture(t, "")
	os.Remove(filepath.Join(repo, ".orchestra", "worker-prompt.md"))
	os.RemoveAll(filepath.Join(repo, ".beads"))
	t.Setenv("HERDR_ENV", "")
	t.Setenv("HERDR_WORKSPACE_ID", "")
	t.Setenv("LIMIT", "many")
	_, p := loadWith(t)
	joined := strings.Join(p, "\n")
	for _, want := range []string{"Not running inside a Herdr pane", "Worker prompt not found", "orchestra init",
		"No Beads database", "LIMIT must be a whole number"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems lack %q:\n%s", want, joined)
		}
	}
	if exitSetup != 2 {
		t.Errorf("setup problems exit with %d", exitSetup)
	}
}

func TestConfigRefusesALinkedWorktreeAndADetachedHead(t *testing.T) {
	repo := configFixture(t, "")
	_, git := gitRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	git(repo, "worktree", "add", "-q", "-b", "other", wt)
	t.Chdir(wt)
	if _, p := loadWith(t); !strings.Contains(strings.Join(p, "\n"), "linked worktree") {
		t.Errorf("worktree: %v", p)
	}
	t.Chdir(repo)
	git(repo, "checkout", "-q", "--detach")
	if _, p := loadWith(t); !strings.Contains(strings.Join(p, "\n"), "detached HEAD") {
		t.Errorf("detached: %v", p)
	}
}

// The exit codes are part of orchestra's interface (scripts and the skill rely on them).
func TestExitCodes(t *testing.T) {
	for name, pair := range map[string][2]int{
		"ok": {exitOK, 0}, "setup": {exitSetup, 2}, "stuck": {exitStuck, 3}, "tool": {exitTool, 4},
		"dirty": {exitDirty, 5}, "merge": {exitMerge, 6}, "interrupted": {exitInterrupted, 130},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %d, want %d", name, pair[0], pair[1])
		}
	}
}
