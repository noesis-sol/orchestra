package dispatch

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/mcp"
)

// chosenServers are a project's MCP servers, as setup resolves them; postgres's definition holds a
// secret.
func chosenServers() *[]mcp.Server {
	return &[]mcp.Server{
		{Name: "postgres", Scope: mcp.ScopeLocal, Type: "stdio",
			Definition: json.RawMessage(`{"command":"pg-mcp","env":{"PGPASSWORD":"secret"}}`)},
		{Name: "exa", Scope: mcp.ScopeUser, Type: "http", Definition: json.RawMessage(`{"type":"http","url":"https://exa.example"}`)},
	}
}

// mcpConfigSeen records the worker's .orchestra/run/mcp.json, as it finds it once started, before
// it finishes as finishes(file) does.
func mcpConfigSeen(file string, seen *mcpFile) behaviour {
	return func(w *fakeWorker) AgentState {
		path := filepath.Join(w.wt, ".orchestra", "run", "mcp.json")
		seen.path = path
		if fi, err := os.Stat(path); err == nil {
			seen.exists, seen.mode = true, fi.Mode().Perm()
			seen.content, _ = os.ReadFile(path)
		}
		return finishes(file)(w)
	}
}

type mcpFile struct {
	path    string
	exists  bool
	mode    os.FileMode
	content []byte
}

// With mcp_servers set, a Claude worker is started, in both ways, with --strict-mcp-config and
// --mcp-config naming a file only its owner reads that holds exactly the chosen servers, and with
// no definition on its command line.
func TestWorkersGetOnlyTheChosenMCPServers(t *testing.T) {
	t.Parallel()
	for _, launchFails := range []bool{false, true} {
		h := newHarness(t)
		h.reporter = fakeReporter{}
		h.cfg.MCP = chosenServers()
		h.herdr.launchFails["A"] = launchFails
		h.beads.add("A", "first", 1)
		var seen mcpFile
		h.worker("A", mcpConfigSeen("a.txt", &seen))
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		starts := h.herdr.argsFor("A")
		if len(starts) == 0 {
			t.Fatal("no worker started")
		}
		for _, args := range starts {
			i := slices.Index(args, "--mcp-config")
			if i < 0 || i+2 >= len(args) || args[i+1] != seen.path || args[i+2] != "--strict-mcp-config" {
				t.Errorf("launch fails %v: args %q, want --mcp-config %s --strict-mcp-config", launchFails, args, seen.path)
			}
			if args[0] == "launch" && !strings.Contains(args[len(args)-1], "prompt.md") {
				t.Errorf("launch fails %v: the prompt isn't last: %q", launchFails, args)
			}
			for _, a := range args {
				if strings.Contains(a, "secret") || strings.Contains(a, "pg-mcp") || strings.Contains(a, "exa.example") {
					t.Errorf("launch fails %v: a definition is on the command line: %q", launchFails, args)
				}
			}
		}
		if !seen.exists || seen.mode != 0o600 {
			t.Fatalf("launch fails %v: mcp.json exists %v, mode %v", launchFails, seen.exists, seen.mode)
		}
		var got struct {
			MCPServers map[string]json.RawMessage `json:"mcpServers"`
		}
		if err := json.Unmarshal(seen.content, &got); err != nil {
			t.Fatal(err)
		}
		compact := func(raw json.RawMessage) string {
			var b bytes.Buffer
			_ = json.Compact(&b, raw)
			return b.String()
		}
		if len(got.MCPServers) != 2 || compact(got.MCPServers["postgres"]) != `{"command":"pg-mcp","env":{"PGPASSWORD":"secret"}}` ||
			compact(got.MCPServers["exa"]) != `{"type":"http","url":"https://exa.example"}` {
			t.Errorf("launch fails %v: mcp.json:\n%s", launchFails, seen.content)
		}
		if logged := h.logged(); !strings.Contains(logged, "MCP servers: postgres, exa)") || strings.Contains(logged, mcpUnchosen) {
			t.Errorf("launch fails %v: log:\n%s", launchFails, logged)
		}
	}
}

// mcp_servers: [] starts a worker with --strict-mcp-config alone: no servers, and no file.
func TestNoMCPServersChosenGivesWorkersNone(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.reporter = fakeReporter{}
	h.cfg.MCP = &[]mcp.Server{}
	h.beads.add("A", "first", 1)
	var seen mcpFile
	h.worker("A", mcpConfigSeen("a.txt", &seen))
	if o, code := h.run(); code != ExitOK {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	for _, args := range h.herdr.argsFor("A") {
		if !slices.Contains(args, "--strict-mcp-config") || slices.Contains(args, "--mcp-config") {
			t.Errorf("args %q", args)
		}
	}
	if seen.exists {
		t.Errorf("%s written", seen.path)
	}
	if logged := h.logged(); !strings.Contains(logged, "MCP servers: none)") {
		t.Errorf("log:\n%s", logged)
	}
}

// Without mcp_servers a worker starts as before, with no MCP arguments, and the run warns once that
// workers get every server.
func TestUnchosenMCPServersKeepTheArgumentsAndWarnOnce(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.reporter = fakeReporter{}
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A", finishes("a.txt"))
	h.worker("B", finishes("b.txt"))
	if o, code := h.run(); code != ExitOK {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	for _, id := range []string{"A", "B"} {
		for _, args := range h.herdr.argsFor(id) {
			want := []string{"launch", "--settings", filepath.Join(h.worktree(id), "hooks.json"),
				"Your instructions for ticket " + id + " are in .orchestra/run/prompt.md in this directory. " +
					"Read that file and follow it exactly."}
			if !equal(args, want) {
				t.Errorf("%s: args %q, want %q", id, args, want)
			}
		}
	}
	logged := h.logged()
	if n := strings.Count(logged, mcpUnchosen); n != 1 || !strings.Contains(logged, "MCP servers: all, not configured)") {
		t.Errorf("warned %d times:\n%s", n, logged)
	}
}

// When Herdr refuses the arguments, a worker is started without its prompt and reports but never
// without its MCP arguments: it would get every server. The run stops instead.
func TestARefusedMCPArgumentStopsTheRun(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.reporter = fakeReporter{}
	h.cfg.MCP = chosenServers()
	h.herdr.launchFails["A"] = true
	h.herdr.refuseArgs = true
	h.beads.add("A", "first", 1)
	o, code := h.run()
	if code != ExitTool || !strings.Contains(o.Final(), "Herdr refused the arguments giving it its MCP servers") {
		t.Fatalf("exit %d, final %q", code, o.Final())
	}
	for _, args := range h.herdr.argsFor("A") {
		if !slices.Contains(args, "--strict-mcp-config") {
			t.Errorf("started without its MCP arguments: %q", args)
		}
	}
}

func TestMCPLabelAndWarning(t *testing.T) {
	for _, tc := range []struct {
		kind  string
		mcp   *[]mcp.Server
		label string
		warns bool
	}{
		{"claude", nil, "all, not configured", true},
		{"claude", &[]mcp.Server{}, "none", false},
		{"claude", chosenServers(), "postgres, exa", false},
		{"codex", nil, "not passed to codex workers", false},
		{"codex", chosenServers(), "not passed to codex workers", false},
	} {
		c := Config{AgentKind: tc.kind, MCP: tc.mcp}
		if got := c.mcpLabel(); got != tc.label {
			t.Errorf("%s %v: label %q, want %q", tc.kind, tc.mcp, got, tc.label)
		}
		if (c.MCPWarning() != "") != tc.warns {
			t.Errorf("%s %v: warning %q", tc.kind, tc.mcp, c.MCPWarning())
		}
	}
}
