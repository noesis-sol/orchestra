package main

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
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
		"PROMPT_AT_LAUNCH", "LIMIT", "DONE_SO_FAR", "AGENT_KIND", "ORCHESTRA_CONCURRENT", "TICKET_LIMIT", "WORKSPACE"} {
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
func loadWith(t *testing.T, args ...string) (options, []string) {
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

func TestConfigTicketLimitPrecedence(t *testing.T) {
	configFixture(t, `{"concurrent": 1, "ticket_limit": "2h"}`)
	if c, p := loadWith(t); len(p) > 0 || c.TicketLimit != 2*time.Hour {
		t.Errorf("settings: %s %v", c.TicketLimit, p)
	}
	t.Setenv("TICKET_LIMIT", "90m")
	if c, _ := loadWith(t); c.TicketLimit != 90*time.Minute {
		t.Errorf("the environment overrides settings: %s", c.TicketLimit)
	}
	if c, _ := loadWith(t, "--ticket-limit", "0"); c.TicketLimit != 0 {
		t.Errorf("--ticket-limit 0 turns it off: %s", c.TicketLimit)
	}
	if _, p := loadWith(t, "--ticket-limit", "-1h"); len(p) != 1 || !strings.Contains(p[0], "--ticket-limit must not be negative") {
		t.Errorf("negative: %v", p)
	}
	t.Setenv("TICKET_LIMIT", "soon")
	if c, p := loadWith(t, "--ticket-limit", "3h"); len(p) > 0 || c.TicketLimit != 3*time.Hour {
		t.Errorf("the flag over an invalid variable: %s %v", c.TicketLimit, p)
	}
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "TICKET_LIMIT must be a duration") {
		t.Errorf("invalid variable: %v", p)
	}
	t.Setenv("TICKET_LIMIT", "")
	os.WriteFile(".orchestra/settings.json", []byte(`{"ticket_limit": "2 hours"}`), 0o644)
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "ticket_limit must be a duration") {
		t.Errorf("invalid setting: %v", p)
	}
	os.Remove(".orchestra/settings.json")
	if c, p := loadWith(t); len(p) > 0 || c.TicketLimit != 0 {
		t.Errorf("no settings: %s %v", c.TicketLimit, p)
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
	if dispatch.ExitSetup != 2 {
		t.Errorf("setup problems exit with %d", dispatch.ExitSetup)
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
		"ok": {dispatch.ExitOK, 0}, "setup": {dispatch.ExitSetup, 2}, "stuck": {dispatch.ExitStuck, 3}, "tool": {dispatch.ExitTool, 4},
		"dirty": {dispatch.ExitDirty, 5}, "merge": {dispatch.ExitMerge, 6}, "interrupted": {dispatch.ExitInterrupted, 130},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %d, want %d", name, pair[0], pair[1])
		}
	}
}

// A flag replaces its variable, so an invalid variable under a flag is no problem; the flags are
// held to the variables' rules.
func TestConfigFlagsOverrideAndAreValidated(t *testing.T) {
	configFixture(t, "")
	t.Setenv("LIMIT", "abc")
	t.Setenv("DONE_SO_FAR", "-2")
	t.Setenv("ORCHESTRA_CONCURRENT", "x")
	if c, p := loadWith(t, "-limit", "5", "-done-so-far", "1", "-c", "2"); len(p) > 0 || c.Limit != 5 || c.DoneSoFar != 1 || c.Concurrency != 2 {
		t.Errorf("flags over invalid variables: %+v %v", c.Config, p)
	}
	if _, p := loadWith(t, "--concurrent", "2"); len(p) != 2 || !strings.Contains(p[0], "LIMIT") || !strings.Contains(p[1], "DONE_SO_FAR") {
		t.Errorf("variables without flags: %v", p)
	}
	t.Setenv("LIMIT", "")
	t.Setenv("DONE_SO_FAR", "")
	t.Setenv("ORCHESTRA_CONCURRENT", "")
	_, p := loadWith(t, "-limit", "-1", "-done-so-far", "-3", "-c", "-1")
	joined := strings.Join(p, "\n")
	for _, want := range []string{"-limit must be a whole number (got -1)", "-done-so-far must be a whole number (got -3)", "between 1 and"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems lack %q:\n%s", want, joined)
		}
	}
}

// A relative WT_ROOT, like a relative WORKER_PROMPT, is relative to the repository, wherever in it
// orchestra runs.
func TestConfigRelativeWorktreesFromASubdirectory(t *testing.T) {
	repo := configFixture(t, "")
	os.MkdirAll(filepath.Join(repo, "sub", "dir"), 0o755)
	t.Chdir(filepath.Join(repo, "sub", "dir"))
	t.Setenv("WT_ROOT", "../wt")
	c, p := loadWith(t)
	if want := filepath.Join(filepath.Dir(repo), "wt"); len(p) > 0 || !samePath(c.WTRoot, want) {
		t.Errorf("WT_ROOT = %s, want %s (%v)", c.WTRoot, want, p)
	}
	if c, _ := loadWith(t, "-worktrees", "../wt2"); !samePath(c.WTRoot, filepath.Join(filepath.Dir(repo), "wt2")) {
		t.Errorf("-worktrees = %s", c.WTRoot)
	}
}

// Worktrees inside the repository are refused however the path reaches it.
func TestConfigRefusesWorktreesInsideTheRepository(t *testing.T) {
	repo := configFixture(t, "")
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	cases := []string{repo, filepath.Join(repo, "wt"), "wt", filepath.Join(link, "wt"), link}
	if runtime.GOOS == "darwin" {
		cases = append(cases, filepath.Join(strings.ToUpper(repo), "wt"))
	}
	for _, root := range cases {
		t.Setenv("WT_ROOT", root)
		if _, p := loadWith(t); !strings.Contains(strings.Join(p, "\n"), "must be outside the repository") {
			t.Errorf("WT_ROOT=%s accepted: %v", root, p)
		}
	}
	for _, root := range []string{repo + "-worktrees", filepath.Join(filepath.Dir(link), "elsewhere")} {
		t.Setenv("WT_ROOT", root)
		if _, p := loadWith(t); len(p) > 0 {
			t.Errorf("WT_ROOT=%s refused: %v", root, p)
		}
	}
}
