package mcp

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverReadsEveryScopeFromTheConfigFiles(t *testing.T) {
	home, repo, main := t.TempDir(), t.TempDir(), t.TempDir()
	config := filepath.Join(home, ".claude.json")
	// The shapes of ~/.claude.json: user scope at the top, local scope per project, and the claude.ai
	// connectors this machine has connected to.
	write(t, config, `{
		"numStartups": 3,
		"mcpServers": {
			"firecrawl": {"type": "stdio", "command": "npx", "args": ["-y", "firecrawl-mcp"], "env": {"FIRECRAWL_API_KEY": "secret"}},
			"browserbase": {"command": "npx", "args": ["-y", "@browserbasehq/mcp-server-browserbase"]},
			"postgres": {"type": "http", "url": "https://user.example/mcp"}
		},
		"projects": {
			"`+main+`": {"mcpServers": {"postgres": {"type": "stdio", "command": "pg-mcp"}}},
			"/somewhere/else": {"mcpServers": {"digitalocean": {"command": "do-mcp"}}},
			"`+repo+`": {"allowedTools": []}
		},
		"claudeAiMcpEverConnected": ["claude.ai Gmail", "claude.ai Google Drive"]
	}`)
	// Project scope: the repository's .mcp.json.
	write(t, filepath.Join(repo, ".mcp.json"), `{"mcpServers": {
		"exa": {"type": "sse", "url": "https://project.example/sse"},
		"firecrawl": {"type": "http", "url": "https://project.example/firecrawl"}
	}}`)

	servers, err := Discover(config, repo, main)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, s := range servers {
		got = append(got, s.Name+" "+s.Scope+" "+s.Type)
	}
	want := []string{
		"browserbase user stdio", // no type: stdio, as Claude Code assumes
		"exa project sse",
		"firecrawl project http", // project scope over user scope
		"postgres local stdio",   // local scope, keyed by the main checkout, over user scope
		"claude.ai Gmail claude.ai ",
		"claude.ai Google Drive claude.ai ",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("servers:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if s, _ := Find(servers, "postgres"); !strings.Contains(string(s.Definition), "pg-mcp") {
		t.Errorf("postgres definition = %s", s.Definition)
	}
	if s, ok := Find(servers, "claude.ai Gmail"); !ok || s.Available() || s.Definition != nil {
		t.Errorf("a claude.ai connector = %+v, %v; want it listed, not available", s, ok)
	}
	if _, ok := Find(servers, "digitalocean"); ok {
		t.Error("another project's local server is offered")
	}
}

func TestDiscoverWithoutConfigFilesFindsNothing(t *testing.T) {
	servers, err := Discover(filepath.Join(t.TempDir(), ".claude.json"), t.TempDir())
	if err != nil || len(servers) != 0 {
		t.Errorf("servers = %v, err = %v", servers, err)
	}
	if servers, err = Discover(""); err != nil || len(servers) != 0 {
		t.Errorf("no config, no repository: servers = %v, err = %v", servers, err)
	}
}

func TestDiscoverReportsAnUnreadableConfig(t *testing.T) {
	repo := t.TempDir()
	write(t, filepath.Join(repo, ".mcp.json"), `{"mcpServers": [`)
	if _, err := Discover("", repo); err == nil || !strings.Contains(err.Error(), ".mcp.json") {
		t.Errorf("err = %v, want one naming .mcp.json", err)
	}
}

func TestDiscoverMatchesTheRepositoryThroughASymlink(t *testing.T) {
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(dir, ".claude.json")
	write(t, config, `{"projects": {"`+repo+`": {"mcpServers": {"postgres": {"command": "pg-mcp"}}}}}`)
	servers, err := Discover(config, link)
	if s, ok := Find(servers, "postgres"); err != nil || !ok || s.Scope != ScopeLocal {
		t.Errorf("servers = %v, err = %v", servers, err)
	}
}

func TestUserConfigFollowsClaudeConfigDir(t *testing.T) {
	env := map[string]string{"HOME": "/home/me"}
	if got := UserConfig(func(k string) string { return env[k] }); got != "/home/me/.claude.json" {
		t.Errorf("HOME: %s", got)
	}
	env["CLAUDE_CONFIG_DIR"] = "/etc/claude"
	if got := UserConfig(func(k string) string { return env[k] }); got != "/etc/claude/.claude.json" {
		t.Errorf("CLAUDE_CONFIG_DIR: %s", got)
	}
	if got := UserConfig(func(string) string { return "" }); got != "" {
		t.Errorf("neither: %s", got)
	}
}

func TestParseNames(t *testing.T) {
	for list, want := range map[string][]string{
		"":                           {},
		" ":                          {},
		"postgres":                   {"postgres"},
		" postgres, firecrawl ,,exa": {"postgres", "firecrawl", "exa"},
		"exa,exa":                    {"exa"},
	} {
		if got := ParseNames(list); !reflect.DeepEqual(got, want) {
			t.Errorf("ParseNames(%q) = %#v, want %#v", list, got, want)
		}
	}
}
