package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// configFixture is a repository set up for orchestra (.orchestra/ with a prompt, Beads) inside a
// Herdr pane, with the working directory in it. loadConfig reads the process's flags and
// environment, so each test resets them.
func configFixture(t *testing.T, settings string) string {
	t.Helper()
	repo, _ := gitRepo(t)
	hostTools(t)
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
	chdir(t, repo)
	for _, k := range []string{"WORKER_PROMPT", "NOTIFY", "WT_ROOT", "TRIAGE", "REVIEW", "ORGAN_MODEL", "ORGAN_EFFORT", "WORKER_EFFORT",
		"PROMPT_AT_LAUNCH", "LIMIT", "DONE_SO_FAR", "AGENT_KIND", "ORCHESTRA_CONCURRENT", "TICKET_LIMIT", "ORCHESTRA_CHECK_TIMEOUT", "WORKSPACE", "ORCHESTRA_TICKET", "ORCHESTRA_TERMINAL", "TMUX", "TMUX_PANE", "CMUX_WORKSPACE_ID", "CMUX_SURFACE_ID"} {
		setenv(t, k, "")
	}
	setenv(t, "HERDR_ENV", "1")
	setenv(t, "HERDR_WORKSPACE_ID", "w7Q")
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
	c, problems, err := loadConfig(testCtx(context.Background(), t), workDir(t), args, getenv(t), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	return c, problems
}

// Claude Code turns Chrome on by itself, so workers are started with --no-chrome once the project
// chose its MCP servers without it, and --chrome when it chose it.
func TestConfigKeepsChromeOutOfWorkersUnlessChosen(t *testing.T) {
	t.Parallel()
	configFixture(t, `{"concurrent": 1}`)
	if err := os.WriteFile(filepath.Join(workDir(t), ".mcp.json"), []byte(`{"mcpServers": {"exa": {"type": "http", "url": "https://exa.example"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ settings, want string }{
		{`{}`, "--effort high"}, // not chosen: whatever Claude Code finds
		{`{"mcp_servers": []}`, "--no-chrome --effort high"},
		{`{"mcp_servers": ["exa"]}`, "--no-chrome --effort high"},
		{`{"mcp_servers": ["exa", "claude-in-chrome"]}`, "--chrome --effort high"},
	} {
		if err := os.WriteFile(filepath.Join(workDir(t), ".orchestra", "settings.json"), []byte(tc.settings), 0o644); err != nil {
			t.Fatal(err)
		}
		if c, p := loadWith(t); len(p) > 0 || strings.Join(c.WorkerArgs, " ") != tc.want {
			t.Errorf("%s: worker arguments %q, want %q (problems %v)", tc.settings, c.WorkerArgs, tc.want, p)
		}
	}
}

func TestConfigDefaultsAndLayout(t *testing.T) {
	t.Parallel()
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
	setenv(t, "LIMIT", "5")
	setenv(t, "TRIAGE", "0")
	if c, _ := loadWith(t, "--workspace", "w1A"); c.Limit != 5 || c.Triage || c.Workspace != "w1A" {
		t.Errorf("environment and flags: limit %d triage %v workspace %s", c.Limit, c.Triage, c.Workspace)
	}
	setenv(t, "WORKSPACE", "ignored")
	if c, _ := loadWith(t); c.Workspace != "w7Q" {
		t.Errorf("WORKSPACE is no longer read: %s", c.Workspace)
	}
}

func TestConfigLegacyLayout(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	repo := configFixture(t, "{}") // set up, so the missing prompt and Beads are listed (see not_set_up_test.go)
	if err := os.Remove(filepath.Join(repo, ".orchestra", "worker-prompt.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(repo, ".beads")); err != nil {
		t.Fatal(err)
	}
	setenv(t, "HERDR_ENV", "")
	setenv(t, "HERDR_WORKSPACE_ID", "")
	setenv(t, "ORCHESTRA_TERMINAL", "herdr")
	setenv(t, "LIMIT", "many")
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
	t.Parallel()
	repo := configFixture(t, "")
	_, git := gitRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	git(repo, "worktree", "add", "-q", "-b", "other", wt)
	chdir(t, wt)
	if _, p := loadWith(t); !strings.Contains(strings.Join(p, "\n"), "linked worktree") {
		t.Errorf("worktree: %v", p)
	}
	chdir(t, repo)
	git(repo, "checkout", "-q", "--detach")
	if _, p := loadWith(t); !strings.Contains(strings.Join(p, "\n"), "detached HEAD") {
		t.Errorf("detached: %v", p)
	}
}

// A relative WT_ROOT, like a relative WORKER_PROMPT, is relative to the repository, wherever in it
// orchestra runs.
func TestConfigRelativeWorktreesFromASubdirectory(t *testing.T) {
	t.Parallel()
	repo := configFixture(t, "")
	if err := os.MkdirAll(filepath.Join(repo, "sub", "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	chdir(t, filepath.Join(repo, "sub", "dir"))
	setenv(t, "WT_ROOT", "../wt")
	c, p := loadWith(t)
	if want := filepath.Join(filepath.Dir(repo), "wt"); len(p) > 0 || !samePath(c.WTRoot, want) {
		t.Errorf("WT_ROOT = %s, want %s (%v)", c.WTRoot, want, p)
	}
	if c, _ := loadWith(t, "--worktrees", "../wt2"); !samePath(c.WTRoot, filepath.Join(filepath.Dir(repo), "wt2")) {
		t.Errorf("--worktrees = %s", c.WTRoot)
	}
}

// Worktrees inside the repository are refused however the path reaches it.
func TestConfigRefusesWorktreesInsideTheRepository(t *testing.T) {
	t.Parallel()
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
		setenv(t, "WT_ROOT", root)
		if _, p := loadWith(t); !strings.Contains(strings.Join(p, "\n"), "must be outside the repository") {
			t.Errorf("WT_ROOT=%s accepted: %v", root, p)
		}
	}
	for _, root := range []string{repo + "-worktrees", filepath.Join(filepath.Dir(link), "elsewhere")} {
		setenv(t, "WT_ROOT", root)
		if _, p := loadWith(t); len(p) > 0 {
			t.Errorf("WT_ROOT=%s refused: %v", root, p)
		}
	}
}

// mcp_servers is resolved to this machine's definitions at start-up; a name it can't give workers
// is a setup problem naming the fix.
func TestConfigResolvesTheWorkersMCPServers(t *testing.T) {
	t.Parallel()
	repo := configFixture(t, `{"mcp_servers": ["postgres", "exa"]}`)
	dir := t.TempDir()
	setenv(t, "CLAUDE_CONFIG_DIR", dir)
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
		if err := os.WriteFile(filepath.Join(workDir(t), ".orchestra", "settings.json"), []byte(s), 0o644); err != nil {
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
	setenv(t, "AGENT_KIND", "codex") // not passed to it, so not resolved
	if c, p := loadWith(t); len(p) > 0 || c.MCP == nil || len(*c.MCP) != 3 || (*c.MCP)[1].Definition != nil {
		t.Errorf("another agent: %v %v", c.MCP, p)
	}
	setenv(t, "AGENT_KIND", "")

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
