package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/mcp"
	"github.com/noesis-sol/orchestra/internal/project"
)

// sessionServers are MCP servers a project chose for its workers.
var sessionServers = []mcp.Server{
	{Name: "docs", Scope: mcp.ScopeUser, Type: "stdio", Definition: json.RawMessage(`{"command":"docs-mcp"}`)},
}

// checkSessionMCP fails t unless args start claude in repo with sessionServers and no other MCP
// servers: --mcp-config naming the definitions in repo's .orchestra/run/mcp.json, then
// --strict-mcp-config.
func checkSessionMCP(t *testing.T, repo string, args []string) {
	t.Helper()
	path := filepath.Join(repo, project.RunPath(project.MCPConfigName))
	i := slices.Index(args, "--mcp-config")
	if i < 0 || i+2 >= len(args) || args[i+1] != path || args[i+2] != "--strict-mcp-config" {
		t.Fatalf("claude ran with %q, want --mcp-config %s --strict-mcp-config", args, path)
	}
	if b := read(t, path); !strings.Contains(b, `"docs"`) || !strings.Contains(b, `"docs-mcp"`) {
		t.Errorf("%s holds:\n%s", path, b)
	}
}

// The headless breakdown gets the MCP servers the project chose for its workers, and no others.
func TestSpecBreakdownGetsTheProjectsMCPServers(t *testing.T) {
	t.Parallel()
	repo, tools := breakdownRepo(t), t.TempDir()
	faketool.Write(t, tools, "bd", filingBd)
	fakeSession(t, tools, "Usage: claude [options]\n", answering(t, specAnswer{Root: "s-1"}))
	onPath(t, tools)
	var out strings.Builder
	h := specBreakdownIn(repo, &out)
	h.servers = sessionServers
	if _, err := h.BreakDown(testCtx(context.Background(), t), breakdownRequest{Spec: testSpec}); err != nil {
		t.Fatal(err)
	}
	checkSessionMCP(t, repo, strings.Split(strings.TrimSuffix(read(t, filepath.Join(tools, "claude-args")), "\n"), "\n"))
}

// A session in a pane, a spec's refinement or a feature's interview, gets the MCP servers the
// project chose for its workers, and no others; with none chosen, no MCP servers at all.
func TestPaneSessionGetsTheProjectsMCPServers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		servers []mcp.Server
	}{{"chosen", sessionServers}, {"none chosen", nil}} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			panes := &fakePanes{agent: []dispatch.AgentState{dispatch.StateIdle}}
			s := paneSession{panes: panes, pane: "%1", repo: repo, servers: tc.servers,
				about: featureSubject{first: paneMessage}}
			if _, err := s.open(context.Background(), "/prompt.md"); err != nil {
				t.Fatal(err)
			}
			args := panes.launched[:slices.Index(panes.launched, "--")]
			if tc.servers != nil {
				checkSessionMCP(t, repo, args)
			} else if slices.Contains(args, "--mcp-config") || !slices.Contains(args, "--strict-mcp-config") {
				t.Errorf("claude started with %q, want --strict-mcp-config alone", panes.launched)
			}
		})
	}
}

// The sessions get the servers of a project whose workers are Claude Code agents; with workers of
// another kind, of whose chosen servers orchestra has only the names, they get none.
func TestSessionMCPIsTheClaudeWorkersServers(t *testing.T) {
	t.Parallel()
	named := []mcp.Server{{Name: "docs"}}
	for _, tc := range []struct {
		name string
		cfg  dispatch.Config
		want []mcp.Server
	}{
		{"claude, chosen", dispatch.Config{AgentKind: "claude", MCP: &sessionServers}, sessionServers},
		{"claude, not chosen", dispatch.Config{AgentKind: "claude"}, nil},
		{"codex, chosen", dispatch.Config{AgentKind: "codex", MCP: &named}, nil},
	} {
		if got := tc.cfg.SessionMCP(); !slices.EqualFunc(got, tc.want, func(a, b mcp.Server) bool {
			return a.Name == b.Name
		}) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}
