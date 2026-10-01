package project

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/mcp"
)

var testServers = []mcp.Server{
	{Name: "postgres", Scope: mcp.ScopeLocal, Type: "stdio", Definition: json.RawMessage(`{"command":"pg-mcp","env":{"PGPASSWORD":"secret"}}`)},
	{Name: "exa", Scope: mcp.ScopeUser, Type: "http", Definition: json.RawMessage(`{"type":"http","url":"https://exa.example"}`)},
	{Name: "claude.ai Gmail", Scope: mcp.ScopeClaudeAI},
}

func names(n ...string) *[]string { return &n }

func TestApplySettingsSavesTheMCPServersNamesOnly(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		mcp  *[]string
		want string // mcp_servers as saved; "" for absent
	}{
		{names("postgres"), `["postgres"]`},
		{names(), `[]`},
		{new([]string), `[]`}, // a nil list is none too
		{nil, ""},
	} {
		if _, err := ApplySettings(repo, Choice{Concurrent: 1, MCP: tc.mcp, Servers: testServers}); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(SettingsPath(repo))
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		var compact bytes.Buffer
		_ = json.Compact(&compact, m["mcp_servers"])
		if got := compact.String(); got != tc.want {
			t.Errorf("mcp_servers = %q, want %q", got, tc.want)
		}
		if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "pg-mcp") {
			t.Errorf("a definition reached settings.json: %s", raw)
		}
		if s, _, _ := LoadSettings(repo); (s.MCPServers == nil) != (tc.mcp == nil) {
			t.Errorf("loaded back: %v", s.MCPServers)
		}
	}
}

func TestDefaultChoiceStartsFromTheMCPSetting(t *testing.T) {
	if c := DefaultChoice(Settings{}, ""); c.MCP != nil {
		t.Errorf("unset: %v", *c.MCP)
	}
	s := Settings{MCPServers: names("exa")}
	c := DefaultChoice(s, "")
	if c.MCP == nil || len(*c.MCP) != 1 || (*c.MCP)[0] != "exa" {
		t.Fatalf("set: %v", c.MCP)
	}
	(*c.MCP)[0] = "changed"
	if (*s.MCPServers)[0] != "exa" {
		t.Error("the choice shares the settings' list")
	}
}

func TestMCPStepExplainsWhatWorkersGet(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    Choice
		kind StepKind
		want []string
	}{
		{"chosen", Choice{MCP: names("postgres", "exa"), Servers: testServers}, StepDone,
			[]string{"workers get postgres (local, stdio), exa (user, http)", "not available to workers (claude.ai connectors): Gmail"}},
		{"none", Choice{MCP: names(), Servers: testServers}, StepDone, []string{"none: workers get no MCP servers"}},
		{"none defined", Choice{MCP: names()}, StepDone, []string{"Claude Code defines none for this project"}},
		{"undefined", Choice{MCP: names("postgres", "redis"), Servers: testServers}, StepCaution,
			[]string{"workers get postgres", "redis isn't defined on this machine (define it: claude mcp add redis", "saved anyway"}},
		{"connector", Choice{MCP: names("claude.ai Gmail"), Servers: testServers}, StepCaution,
			[]string{"chosen: claude.ai Gmail", "is a claude.ai connector, which workers can't get"}},
		{"unasked", Choice{MCPUnasked: true, Servers: testServers}, StepCaution,
			[]string{"not chosen: workers load every MCP server", "not asked: no terminal; --mcp sets them"}},
		{"unreadable", Choice{MCP: names(), ServersErr: errors.New("bad json")}, StepCaution,
			[]string{"couldn't read Claude Code's MCP config: bad json"}},
	} {
		st := MCPStep(tc.c)
		if st.Kind != tc.kind || st.Label != "MCP servers" {
			t.Errorf("%s: %+v", tc.name, st)
		}
		for _, w := range tc.want {
			if !strings.Contains(st.Detail, w) {
				t.Errorf("%s: %q lacks %q", tc.name, st.Detail, w)
			}
		}
	}
}

func TestNextStepsMentionMCPWhenNoneWereChosen(t *testing.T) {
	repo, _ := gitRepo(t)
	hint := func(c Choice) bool {
		return strings.Contains(strings.Join(NextSteps(context.Background(), repo, nil, nil, c), "\n"), "--mcp")
	}
	if !hint(Choice{Servers: testServers}) || !hint(Choice{MCP: names(), Servers: testServers}) {
		t.Error("none chosen with servers defined: no --mcp hint")
	}
	if hint(Choice{MCP: names("exa"), Servers: testServers}) {
		t.Error("servers chosen: --mcp hint")
	}
	if hint(Choice{Servers: testServers[2:]}) {
		t.Error("only claude.ai connectors: --mcp hint")
	}
}

func TestConfigRootsAddTheMainCheckoutOfAWorktree(t *testing.T) {
	repo, git := gitRepo(t)
	if roots := ConfigRoots(context.Background(), repo); len(roots) != 1 || roots[0] != repo {
		t.Errorf("main checkout: %v", roots)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	git(repo, "worktree", "add", "-q", wt)
	roots := ConfigRoots(context.Background(), wt)
	if len(roots) != 2 || roots[0] != wt {
		t.Fatalf("worktree: %v", roots)
	}
	if resolved, _ := filepath.EvalSymlinks(repo); roots[1] != repo && roots[1] != resolved {
		t.Errorf("worktree's main checkout = %s, want %s", roots[1], repo)
	}
}

func TestWriteMCPConfigIsForItsOwnerOnly(t *testing.T) {
	wt := t.TempDir()
	path := filepath.Join(wt, Dir, RunName, MCPConfigName)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("earlier"), 0o644); err != nil { // an earlier worker's, readable by all
		t.Fatal(err)
	}
	got, err := WriteMCPConfig(wt, testServers[:2])
	if err != nil || got != path {
		t.Fatalf("%s %v", got, err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", fi.Mode(), err)
	}
	var f struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &f); err != nil || len(f.MCPServers) != 2 || !strings.Contains(string(f.MCPServers["postgres"]), "secret") {
		t.Errorf("%s %v", b, err)
	}
}
