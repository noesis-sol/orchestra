package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

func TestInitCorrectsConcurrencyTrimsCheckAndKeepsUnknownKeys(t *testing.T) {
	repo := initRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, project.Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(project.SettingsPath(repo), []byte(`{"concurrent": 20, "notes": "ours"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runIn(t, repo, nil, "init", "--check", "  make check \n")
	if err != nil || !strings.Contains(stdout, "Sets this project up for orchestra") {
		t.Fatalf("init: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(read(t, project.SettingsPath(repo))), &m); err != nil {
		t.Fatal(err)
	}
	if m["concurrent"] != float64(1) || m["check_fast"] != project.FastRunner || m["notes"] != "ours" {
		t.Errorf("settings.json = %v", m)
	}
	if runner := read(t, filepath.Join(repo, project.FastRunner)); !strings.Contains(runner, "\nmake check\n") {
		t.Errorf("%s:\n%s", project.FastRunner, runner)
	}
	s, _, _ := project.LoadSettings(repo)
	if _, err := project.ResolveConcurrency(0, s); err != nil {
		t.Errorf("a run after init: %v", err)
	}
}

func TestInitSavesTheCheckTimeout(t *testing.T) {
	repo := initRepo(t)
	stdout, stderr, err := runIn(t, repo, nil, "init", "--check", "make check", "--check-timeout", "90s", "-c", "1")
	if err != nil || !strings.Contains(strings.Join(strings.Fields(stdout), " "), "check-fast: make check, stopped after 1m30s") {
		t.Fatalf("init: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	s, _, _ := project.LoadSettings(repo)
	if d, err := project.ResolveCheckTimeout(0, false, s); s.CheckFastTimeout != "1m30s" || err != nil || d != 90*time.Second {
		t.Errorf("settings = %+v, a run gets %s, %v", s, d, err)
	}
	_, stderr, err = runIn(t, repo, nil, "init", "--check-timeout", "0")
	if exitOf(err) != dispatch.ExitSetup || !strings.Contains(stderr, "--check-timeout must be a positive duration") {
		t.Errorf("--check-timeout 0: exit = %d, stderr:\n%s", exitOf(err), stderr)
	}
}

func TestInitChoosesWorkersMCPServersWithoutATerminal(t *testing.T) {
	repo := initRepo(t)
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{
		"mcpServers": {"firecrawl": {"type": "stdio", "command": "npx", "env": {"FIRECRAWL_API_KEY": "secret"}}},
		"claudeAiMcpEverConnected": ["claude.ai Gmail"]
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".mcp.json"), []byte(`{"mcpServers": {"postgres": {"command": "pg-mcp"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"HOME": home}
	settings := func() string {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(read(t, project.SettingsPath(repo))), &m); err != nil {
			t.Fatal(err)
		}
		return strings.Join(strings.Fields(string(m["mcp_servers"])), "")
	}

	// No terminal, no --mcp, first run: left unset, and the summary says so.
	stdout, stderr, err := runIn(t, repo, env, "init", "--check", "make check")
	if err != nil || settings() != "" || !strings.Contains(stdout, "not asked: no terminal; --mcp sets them") ||
		!strings.Contains(stdout, "orchestra init --mcp") {
		t.Fatalf("unset: %v, mcp_servers %q\nstdout:\n%s\nstderr:\n%s", err, settings(), stdout, stderr)
	}
	// --mcp saves the names, and nothing from the definitions.
	stdout, stderr, err = runIn(t, repo, env, "init", "--mcp", "postgres, firecrawl")
	if err != nil || settings() != `["postgres","firecrawl"]` {
		t.Fatalf("--mcp: %v, mcp_servers %q\nstdout:\n%s\nstderr:\n%s", err, settings(), stdout, stderr)
	}
	if raw := read(t, project.SettingsPath(repo)); strings.Contains(raw, "secret") || strings.Contains(raw, "pg-mcp") {
		t.Errorf("a definition reached settings.json: %s", raw)
	}
	plain := strings.Join(strings.Fields(stdout), " ")
	for _, want := range []string{"postgres (project, stdio), firecrawl (user, stdio)", "(claude.ai connectors): Gmail"} {
		if !strings.Contains(plain, want) {
			t.Errorf("summary lacks %q:\n%s", want, stdout)
		}
	}
	// Without --mcp again, the setting is kept.
	if _, _, err = runIn(t, repo, env, "init"); err != nil || settings() != `["postgres","firecrawl"]` {
		t.Errorf("kept: %v, mcp_servers %q", err, settings())
	}
	// A name this machine doesn't define is reported, and saved.
	stdout, _, err = runIn(t, repo, env, "init", "--mcp", "redis")
	if plain = strings.Join(strings.Fields(stdout), " "); err != nil || settings() != `["redis"]` ||
		!strings.Contains(plain, "claude mcp add redis") {
		t.Errorf("undefined: %v, mcp_servers %q\nstdout:\n%s", err, settings(), stdout)
	}
	// --mcp "" chooses none.
	stdout, _, err = runIn(t, repo, env, "init", "--mcp", "")
	if err != nil || settings() != `[]` || !strings.Contains(stdout, "orchestra init --mcp") {
		t.Errorf("none: %v, mcp_servers %q\nstdout:\n%s", err, settings(), stdout)
	}
}
