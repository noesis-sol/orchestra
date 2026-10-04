package main

import (
	"context"
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
	if err := os.MkdirAll(filepath.Join(repo, ".orchestra"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".beads"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".orchestra", "worker-prompt.md"), []byte("Work on TICKET_ID."), 0o644); err != nil {
		t.Fatal(err)
	}
	if settings != "" {
		if err := os.WriteFile(filepath.Join(repo, ".orchestra", "settings.json"), []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(repo)
	for _, k := range []string{"WORKER_PROMPT", "NOTIFY", "WT_ROOT", "TRIAGE", "REVIEW", "ORGAN_MODEL", "ORGAN_EFFORT", "WORKER_EFFORT",
		"PROMPT_AT_LAUNCH", "LIMIT", "DONE_SO_FAR", "AGENT_KIND", "ORCHESTRA_CONCURRENT", "TICKET_LIMIT", "ORCHESTRA_CHECK_TIMEOUT", "WORKSPACE", "ORCHESTRA_TICKET"} {
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
	resolve := func(p string) string {
		d, err := filepath.EvalSymlinks(filepath.Dir(p))
		if err != nil {
			return p
		}
		return filepath.Join(d, filepath.Base(p))
	}
	return resolve(a) == resolve(b)
}

// loadWith runs loadConfig with these command-line arguments and the process's environment.
func loadWith(t *testing.T, args ...string) (options, []string) {
	t.Helper()
	c, problems, err := loadConfig(context.Background(), args, os.Getenv, io.Discard)
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
	if err := os.Remove(".orchestra/settings.json"); err != nil {
		t.Fatal(err)
	}
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
	if err := os.WriteFile(".orchestra/settings.json", []byte(`{"ticket_limit": "2 hours"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "ticket_limit must be a duration") {
		t.Errorf("invalid setting: %v", p)
	}
	if err := os.Remove(".orchestra/settings.json"); err != nil {
		t.Fatal(err)
	}
	if c, p := loadWith(t); len(p) > 0 || c.TicketLimit != 0 {
		t.Errorf("no settings: %s %v", c.TicketLimit, p)
	}
}

func TestConfigCheckTimeoutPrecedence(t *testing.T) {
	configFixture(t, `{"concurrent": 1, "check_timeout": "5m"}`)
	if c, p := loadWith(t); len(p) > 0 || c.CheckTimeout != 5*time.Minute {
		t.Errorf("settings: %s %v", c.CheckTimeout, p)
	}
	t.Setenv("ORCHESTRA_CHECK_TIMEOUT", "10m")
	if c, _ := loadWith(t); c.CheckTimeout != 10*time.Minute {
		t.Errorf("the environment overrides settings: %s", c.CheckTimeout)
	}
	if c, _ := loadWith(t, "--check-timeout", "45m"); c.CheckTimeout != 45*time.Minute {
		t.Errorf("--check-timeout overrides the environment: %s", c.CheckTimeout)
	}
	if _, p := loadWith(t, "--check-timeout", "0"); len(p) != 1 || !strings.Contains(p[0], "--check-timeout must be a positive duration") {
		t.Errorf("zero: %v", p)
	}
	t.Setenv("ORCHESTRA_CHECK_TIMEOUT", "0")
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "ORCHESTRA_CHECK_TIMEOUT must be a positive duration") {
		t.Errorf("zero variable: %v", p)
	}
	if c, p := loadWith(t, "--check-timeout", "2m"); len(p) > 0 || c.CheckTimeout != 2*time.Minute {
		t.Errorf("the flag over an invalid variable: %s %v", c.CheckTimeout, p)
	}
	t.Setenv("ORCHESTRA_CHECK_TIMEOUT", "")
	if err := os.WriteFile(".orchestra/settings.json", []byte(`{"check_timeout": "5 minutes"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "check_timeout must be a positive duration") {
		t.Errorf("invalid setting: %v", p)
	}
	if err := os.Remove(".orchestra/settings.json"); err != nil {
		t.Fatal(err)
	}
	if c, p := loadWith(t); len(p) > 0 || c.CheckTimeout != 30*time.Minute {
		t.Errorf("no settings: %s %v", c.CheckTimeout, p)
	}
}

func TestConfigExcludeTypes(t *testing.T) {
	configFixture(t, `{"concurrent": 1}`)
	if c, p := loadWith(t); len(p) > 0 || strings.Join(c.ExcludeTypes, " ") != "epic" {
		t.Errorf("default: %v %v", c.ExcludeTypes, p)
	}
	if err := os.WriteFile(".orchestra/settings.json", []byte(`{"exclude_types": ["epic", "decision"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, p := loadWith(t); len(p) > 0 || strings.Join(c.ExcludeTypes, " ") != "epic decision" {
		t.Errorf("settings: %v %v", c.ExcludeTypes, p)
	}
	if err := os.WriteFile(".orchestra/settings.json", []byte(`{"exclude_types": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, p := loadWith(t); len(p) > 0 || len(c.ExcludeTypes) != 0 {
		t.Errorf("none: %v %v", c.ExcludeTypes, p)
	}
	if err := os.WriteFile(".orchestra/settings.json", []byte(`{"exclude_types": ["epic,decision"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "exclude_types must be a list") {
		t.Errorf("invalid setting: %v", p)
	}
}

// Claude Code turns Chrome on by itself, so workers are started with --no-chrome once the project
// chose its MCP servers without it, and --chrome when it chose it.
func TestConfigKeepsChromeOutOfWorkersUnlessChosen(t *testing.T) {
	configFixture(t, `{"concurrent": 1}`)
	for _, tc := range []struct{ settings, want string }{
		{`{}`, ""}, // not chosen: whatever Claude Code finds
		{`{"mcp_servers": []}`, "--no-chrome"},
		{`{"mcp_servers": ["exa"]}`, "--no-chrome"},
		{`{"mcp_servers": ["exa", "claude-in-chrome"]}`, "--chrome"},
	} {
		if err := os.WriteFile(".orchestra/settings.json", []byte(tc.settings), 0o644); err != nil {
			t.Fatal(err)
		}
		if c, p := loadWith(t); len(p) > 0 || strings.Join(c.WorkerArgs, " ") != tc.want {
			t.Errorf("%s: worker arguments %q, want %q (problems %v)", tc.settings, c.WorkerArgs, tc.want, p)
		}
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
	if err := os.RemoveAll(filepath.Join(repo, ".orchestra")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".claude", "worker-prompt.md"), []byte("Work on TICKET_ID."), 0o644); err != nil {
		t.Fatal(err)
	}
	c, p := loadWith(t)
	if len(p) > 0 || !strings.HasSuffix(c.LogPath, ".claude/orchestrate.log") || !strings.HasSuffix(c.ReportsDir, ".claude/orchestrate-reports") {
		t.Errorf("legacy project.Layout: %s %s %v", c.LogPath, c.ReportsDir, p)
	}
}

// Every setup problem is reported, together; main exits with code 2 when there are any.
func TestConfigSetupProblems(t *testing.T) {
	repo := configFixture(t, "{}") // set up, so the missing prompt and Beads are listed (see not_set_up_test.go)
	if err := os.Remove(filepath.Join(repo, ".orchestra", "worker-prompt.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(repo, ".beads")); err != nil {
		t.Fatal(err)
	}
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
	if err := os.MkdirAll(filepath.Join(repo, "sub", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
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

// mcp_servers is resolved to this machine's definitions at start-up; a name it can't give workers
// is a setup problem naming the fix.
func TestConfigResolvesTheWorkersMCPServers(t *testing.T) {
	repo := configFixture(t, `{"mcp_servers": ["postgres", "exa"]}`)
	dir := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", dir)
	user := `{"mcpServers": {"postgres": {"command": "pg-mcp"}}, "claudeAiMcpEverConnected": ["claude.ai Gmail"]}`
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(user), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".mcp.json"), []byte(`{"mcpServers": {"exa": {"type": "http", "url": "https://exa.example"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, p := loadWith(t)
	if len(p) > 0 || c.MCP == nil || len(*c.MCP) != 2 {
		t.Fatalf("resolved: %v %v", c.MCP, p)
	}
	if s := (*c.MCP)[0]; s.Name != "postgres" || string(s.Definition) != `{"command": "pg-mcp"}` {
		t.Errorf("postgres: %+v", s)
	}
	if s := (*c.MCP)[1]; s.Name != "exa" || !strings.Contains(string(s.Definition), "exa.example") {
		t.Errorf("exa: %+v", s)
	}

	settings := func(s string) {
		t.Helper()
		if err := os.WriteFile(".orchestra/settings.json", []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	settings(`{"mcp_servers": ["postgres", "redis", "claude.ai Gmail"]}`)
	_, p = loadWith(t)
	if len(p) != 1 {
		t.Fatalf("unresolvable: %v", p)
	}
	for _, want := range []string{"mcp_servers in .orchestra/settings.json", "redis isn't defined on this machine (define it: claude mcp add redis …)",
		"claude.ai Gmail is a claude.ai connector, which workers can't get", "orchestra init"} {
		if !strings.Contains(p[0], want) {
			t.Errorf("%q lacks %q", p[0], want)
		}
	}
	if strings.Contains(p[0], "postgres") {
		t.Errorf("names a resolved server: %q", p[0])
	}
	t.Setenv("AGENT_KIND", "codex") // not passed to it, so not resolved
	if c, p := loadWith(t); len(p) > 0 || c.MCP == nil || len(*c.MCP) != 3 || (*c.MCP)[1].Definition != nil {
		t.Errorf("another agent: %v %v", c.MCP, p)
	}
	t.Setenv("AGENT_KIND", "")

	settings(`{"mcp_servers": []}`)
	if c, p := loadWith(t); len(p) > 0 || c.MCP == nil || len(*c.MCP) != 0 {
		t.Errorf("none: %v %v", c.MCP, p)
	}
	settings(`{}`)
	if c, p := loadWith(t); len(p) > 0 || c.MCP != nil {
		t.Errorf("unset: %v %v", c.MCP, p)
	}
	if err := os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	settings(`{"mcp_servers": ["postgres"]}`)
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "Cannot read Claude Code's MCP config") {
		t.Errorf("unreadable: %v", p)
	}
}
