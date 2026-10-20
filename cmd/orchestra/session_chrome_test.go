package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/mcp"
)

// checkSessionChrome fails t unless args start claude with Chrome as chrome has it: --chrome or
// --no-chrome, or neither for none.
func checkSessionChrome(t *testing.T, args, chrome []string) {
	t.Helper()
	for _, flag := range []string{"--chrome", "--no-chrome"} {
		if slices.Contains(args, flag) != slices.Contains(chrome, flag) {
			t.Errorf("claude ran with %q, want Chrome as %q", args, chrome)
			return
		}
	}
}

// orchestra's own Claude Code sessions get Chrome as the project's workers do, for the same settings:
// --no-chrome once the project chose its MCP servers without it, --chrome when it chose it, nothing
// when it hasn't chosen.
func TestSessionChromeIsTheWorkersChrome(t *testing.T) {
	t.Parallel()
	configFixture(t, `{"concurrent": 1}`)
	if err := os.WriteFile(filepath.Join(workDir(t), ".mcp.json"),
		[]byte(`{"mcpServers": {"exa": {"type": "http", "url": "https://exa.example"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ settings, want string }{
		{`{}`, ""},
		{`{"mcp_servers": []}`, "--no-chrome"},
		{`{"mcp_servers": ["exa"]}`, "--no-chrome"},
		{`{"mcp_servers": ["exa", "claude-in-chrome"]}`, "--chrome"},
	} {
		if err := os.WriteFile(filepath.Join(workDir(t), ".orchestra", "settings.json"), []byte(tc.settings),
			0o644); err != nil {
			t.Fatal(err)
		}
		c, p := loadWith(t)
		if len(p) > 0 {
			t.Fatalf("%s: %v", tc.settings, p)
		}
		got := strings.Join(c.SessionChrome(), " ")
		if got != tc.want || !strings.HasPrefix(strings.Join(c.WorkerArgs, " "), got) {
			t.Errorf("%s: sessions get %q, want %q, as workers get %q", tc.settings, got, tc.want, c.WorkerArgs)
		}
	}
}

// With workers of another kind, of whose servers orchestra has only the names, the sessions get none
// of them but still Chrome as the project chose it.
func TestSessionChromeFollowsTheNamesForOtherWorkers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		names []string
		want  string
	}{{[]string{"docs"}, "--no-chrome"}, {[]string{"docs", mcp.Chrome}, "--chrome"}} {
		named := []mcp.Server{}
		for _, name := range tc.names {
			named = append(named, mcp.Server{Name: name})
		}
		c := dispatch.Config{AgentKind: "codex", MCP: &named}
		if got := strings.Join(c.SessionChrome(), " "); got != tc.want {
			t.Errorf("%v: sessions get %q, want %q", tc.names, got, tc.want)
		}
	}
}

// The headless breakdown starts claude with Chrome as the project's workers get it.
func TestSpecBreakdownGetsTheWorkersChrome(t *testing.T) {
	t.Parallel()
	for _, chrome := range [][]string{{"--no-chrome"}, {"--chrome"}, nil} {
		repo, tools := breakdownRepo(t), t.TempDir()
		faketool.Write(t, tools, "bd", filingBd)
		fakeSession(t, tools, "Usage: claude [options]\n", answering(t, specAnswer{Root: "s-1"}))
		onPath(t, tools)
		var out strings.Builder
		h := specBreakdownIn(repo, &out)
		h.claude.servers, h.claude.chrome = sessionServers, chrome
		if _, err := h.BreakDown(testCtx(context.Background(), t), breakdownRequest{Spec: testSpec}); err != nil {
			t.Fatal(err)
		}
		args := strings.Split(strings.TrimSuffix(read(t, filepath.Join(tools, "claude-args")), "\n"), "\n")
		checkSessionMCP(t, repo, args)
		checkSessionChrome(t, args, chrome)
	}
}

// A session in a pane, a spec's refinement or a feature's interview, starts claude with Chrome as the
// project's workers get it.
func TestPaneSessionGetsTheWorkersChrome(t *testing.T) {
	t.Parallel()
	for _, chrome := range [][]string{{"--no-chrome"}, {"--chrome"}, nil} {
		repo := t.TempDir()
		panes := &fakePanes{agent: []dispatch.AgentState{dispatch.StateIdle}}
		s := paneSession{panes: panes, pane: "%1", claude: claudeSession{repo: repo, servers: sessionServers, chrome: chrome},
			about: featureSubject{first: paneMessage}}
		if _, err := s.open(context.Background(), "/prompt.md"); err != nil {
			t.Fatal(err)
		}
		args := panes.launched[:slices.Index(panes.launched, "--")]
		checkSessionMCP(t, repo, args)
		checkSessionChrome(t, args, chrome)
	}
}
