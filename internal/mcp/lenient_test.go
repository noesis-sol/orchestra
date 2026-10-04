package mcp

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// names lists servers as "name scope", in Discover's order.
func names(servers []Server) []string {
	var got []string
	for _, s := range servers {
		got = append(got, s.Name+" "+s.Scope)
	}
	return got
}

func TestDiscoverTakesClaudeCodesRecordsOfAnotherShapeAsNotSet(t *testing.T) {
	for _, tc := range []struct {
		name, records string
	}{
		{"connectors as objects", `"claudeAiMcpEverConnected": [{"name": "claude.ai Gmail", "at": 1}]`},
		{"connectors mixed", `"claudeAiMcpEverConnected": ["claude.ai Gmail", {"name": "claude.ai Google Drive"}]`},
		{"connectors as an object", `"claudeAiMcpEverConnected": {"claude.ai Gmail": true}`},
		{"Chrome as a string", `"claudeInChromeDefaultEnabled": "yes"`},
		{"Chrome as an object", `"cachedChromeExtensionInstalled": {"version": "1.2"}`},
		{"Chrome as a number", `"hasCompletedClaudeInChromeOnboarding": 1`},
		{"every record", `"claudeAiMcpEverConnected": [{"name": "claude.ai Gmail"}],
			"claudeInChromeDefaultEnabled": "yes", "cachedChromeExtensionInstalled": [true],
			"hasCompletedClaudeInChromeOnboarding": {"at": 1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, repo := t.TempDir(), t.TempDir()
			config := filepath.Join(home, ".claude.json")
			write(t, config, `{
				"mcpServers": {"firecrawl": {"command": "firecrawl-mcp"}},
				"projects": {"`+repo+`": {"mcpServers": {"postgres": {"command": "pg-mcp"}}}},
				`+tc.records+`
			}`)
			write(t, filepath.Join(repo, ".mcp.json"), `{"mcpServers": {"exa": {"type": "http", "url": "https://exa.example"}}}`)

			servers, err := Discover(config, repo)
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"exa project", "firecrawl user", "postgres local"}
			if got := names(servers); !reflect.DeepEqual(got, want) {
				t.Errorf("servers = %q, want %q: no connectors and no Chrome", got, want)
			}
		})
	}
}

func TestDiscoverReadsTheRecordsItUnderstandsBesideOnesItDoesnt(t *testing.T) {
	config := filepath.Join(t.TempDir(), ".claude.json")
	write(t, config, `{
		"claudeAiMcpEverConnected": ["claude.ai Gmail"],
		"claudeInChromeDefaultEnabled": "yes",
		"cachedChromeExtensionInstalled": true
	}`)
	servers, err := Discover(config, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{Chrome + " " + ScopeBuiltIn, "claude.ai Gmail " + ScopeClaudeAI}
	if got := names(servers); !reflect.DeepEqual(got, want) {
		t.Errorf("servers = %q, want %q", got, want)
	}
}

func TestDiscoverStillFailsOnAConfigItCannotRead(t *testing.T) {
	for name, content := range map[string]string{
		"not JSON":                      `mcpServers = {}`,
		"cut short":                     `{"mcpServers": {"exa": {"command": "exa"}}, "claudeAiMcpEverConnected": [`,
		"mcpServers a list":             `{"mcpServers": ["exa"]}`,
		"mcpServers a string":           `{"mcpServers": "exa"}`,
		"a project's mcpServers a list": `{"projects": {"/repo": {"mcpServers": ["postgres"]}}}`,
		"projects a list":               `{"projects": ["/repo"]}`,
	} {
		t.Run(name, func(t *testing.T) {
			config := filepath.Join(t.TempDir(), ".claude.json")
			write(t, config, content)
			servers, err := Discover(config, t.TempDir())
			if err == nil || !strings.Contains(err.Error(), config) {
				t.Fatalf("servers = %v, err = %v; want an error naming %s", servers, err, config)
			}
			var syntaxErr *json.SyntaxError
			var typeErr *json.UnmarshalTypeError
			if !errors.As(err, &syntaxErr) && !errors.As(err, &typeErr) {
				t.Errorf("err = %v (%T), want a JSON syntax or type error", err, err)
			}
		})
	}
}
