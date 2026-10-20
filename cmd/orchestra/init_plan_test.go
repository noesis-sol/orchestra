package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/organ"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tui"
)

func TestParseInitFlags(t *testing.T) {
	t.Parallel()
	env := func(vars map[string]string) func(string) string { return func(k string) string { return vars[k] } }
	for _, tc := range []struct {
		name   string
		args   []string
		vars   map[string]string
		check  func(initFlags) bool
		code   int
		ok     bool
		stderr string // the start of what it says; "" for nothing
	}{
		{"none", nil, nil, func(f initFlags) bool {
			return !f.checkFast.Given && !f.concurrent.Given && !f.union.Given && !f.prefix.Given && f.agent == "claude"
		}, dispatch.ExitOK, true, ""},
		{"the check's alias, given empty", []string{"--check", ""}, nil, func(f initFlags) bool {
			return f.checkFast.Given && f.checkFast.Value == "" && !f.checkFull.Given
		}, dispatch.ExitOK, true, ""},
		{"the timeout's alias", []string{"--check-timeout", "90s"}, nil, func(f initFlags) bool {
			return f.fastTimeout == project.Given[time.Duration]{Value: 90 * time.Second, Given: true}
		}, dispatch.ExitOK, true, ""},
		{"concurrency's shorthand", []string{"-c", "3"}, nil, func(f initFlags) bool {
			return f.concurrent == project.Given[int]{Value: 3, Given: true}
		}, dispatch.ExitOK, true, ""},
		{"declined", []string{"--changelog-union=false", "--verifier=false"}, nil, func(f initFlags) bool {
			return f.union.Given && !f.union.Value && f.verifier.Given && !f.verifier.Value && !f.install.Given
		}, dispatch.ExitOK, true, ""},
		{"the agent from AGENT_KIND", nil, map[string]string{"AGENT_KIND": "codex"}, func(f initFlags) bool {
			return f.agent == "codex"
		}, dispatch.ExitOK, true, ""},
		{"the agent's flag over AGENT_KIND", []string{"--agent", "claude"}, map[string]string{"AGENT_KIND": "codex"},
			func(f initFlags) bool { return f.agent == "claude" }, dispatch.ExitOK, true, ""},
		{"force", []string{"--force"}, nil, func(f initFlags) bool { return f.force }, dispatch.ExitOK, true, ""},
		{"help", []string{"-h"}, nil, nil, dispatch.ExitOK, false, "Usage: orchestra init"},
		{"a zero time limit, by its alias", []string{"--check-timeout", "0"}, nil, nil, dispatch.ExitSetup, false,
			"orchestra init: --check-timeout must be a positive duration such as 5m\n"},
		{"two zero time limits", []string{"--check-fast-timeout=0", "--check-timeout=0"}, nil, nil,
			dispatch.ExitSetup, false, "orchestra init: --check-fast-timeout must be a positive duration such as 5m\n"},
		{"concurrency out of range, by its shorthand", []string{"-c", "0"}, nil, nil, dispatch.ExitSetup, false,
			"orchestra init: --concurrent must be between 1 and 16\n"},
		{"an argument", []string{"now"}, nil, nil, dispatch.ExitSetup, false,
			"orchestra init: unexpected argument \"now\" (see orchestra init -h)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr strings.Builder
			f, code, ok := parseInitFlags(tc.args, env(tc.vars), &stderr)
			if code != tc.code || ok != tc.ok {
				t.Errorf("code %d, ok %v; want %d, %v", code, ok, tc.code, tc.ok)
			}
			if tc.check != nil && !tc.check(f) {
				t.Errorf("flags %+v", f)
			}
			if got := stderr.String(); tc.stderr == "" && got != "" || !strings.HasPrefix(got, tc.stderr) {
				t.Errorf("stderr %q, want it to start with %q", got, tc.stderr)
			}
		})
	}
}

func TestInitChoiceTakesTheFlagsOverTheProject(t *testing.T) {
	t.Parallel()
	given := func(v string) project.Given[string] { return project.Given[string]{Value: v, Given: true} }
	for _, tc := range []struct {
		name     string
		settings project.Settings
		checkSh  bool // the project has scripts/check.sh
		flags    initFlags
		check    func(project.Choice) bool
	}{
		{"the settings' check", project.Settings{CheckFast: "make check", Concurrency: 2}, false, initFlags{},
			func(c project.Choice) bool {
				return c.FastCommand() == "make check" && c.CheckFrom == "settings" && !c.ReplaceFast &&
					c.Concurrent == 2 && !c.Unasked.Concurrent
			}},
		{"the project's own check", project.Settings{}, true, initFlags{}, func(c project.Choice) bool {
			return c.FastCommand() == project.CheckScript && c.CheckFrom == project.CheckScript
		}},
		{"--check-fast over the project's own check", project.Settings{}, true,
			initFlags{checkFast: given("go test ./...")}, func(c project.Choice) bool {
				return c.FastCommand() == "go test ./..." && c.CheckFrom == "--check-fast" && c.ReplaceFast
			}},
		{"--check-full", project.Settings{CheckFull: "make e2e"}, false, initFlags{checkFull: given("")},
			func(c project.Choice) bool { return c.Full != nil && len(*c.Full) == 0 && c.ReplaceFull }},
		{"concurrency fallen back", project.Settings{}, false, initFlags{}, func(c project.Choice) bool {
			return c.Concurrent == 1 && c.Unasked.Concurrent
		}},
		{"--concurrent", project.Settings{}, false, initFlags{concurrent: project.Given[int]{Value: 4, Given: true}},
			func(c project.Choice) bool { return c.Concurrent == 4 && !c.Unasked.Concurrent }},
		{"time limits over ones the settings had wrong",
			project.Settings{CheckFastTimeout: "soon", CheckFullTimeout: "0"}, false,
			initFlags{fastTimeout: project.Given[time.Duration]{Value: 90 * time.Second, Given: true},
				fullTimeout: project.Given[time.Duration]{Value: 2 * time.Hour, Given: true}},
			func(c project.Choice) bool {
				return c.CheckFastTimeout == "1m30s" && c.ReplacedTimeout == "" && c.CheckFullTimeout == "2h" &&
					c.ReplacedFullTimeout == ""
			}},
		{"--mcp and --setup", project.Settings{Setup: "npm ci"}, false,
			initFlags{mcp: given("postgres, firecrawl"), setup: given("  "), agent: "codex"},
			func(c project.Choice) bool {
				return c.MCP != nil && strings.Join(*c.MCP, ",") == "postgres,firecrawl" && c.Setup == "" &&
					c.SetupWas == "npm ci" && c.Agent == "codex"
			}},
		{"--mcp none", project.Settings{}, false, initFlags{mcp: given("")}, func(c project.Choice) bool {
			return c.MCP != nil && len(*c.MCP) == 0
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := t.TempDir()
			if tc.checkSh {
				if err := os.MkdirAll(filepath.Join(repo, "scripts"), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(repo, project.CheckScript), []byte("#!/bin/sh\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if c := initChoice(repo, tc.flags, tc.settings); !tc.check(c) {
				t.Errorf("choice %+v", c)
			}
		})
	}
}

// Outside a terminal init asks nothing: what it offers as yes is declined, and the summary says how
// to answer what it couldn't ask.
func TestAskOrDefaultOutsideATerminalDeclinesTheOffers(t *testing.T) {
	t.Parallel()
	c := project.Choice{Union: true, InstallBeads: true, Verifier: true, Unasked: project.Questions{Concurrent: true}}
	ask := project.Questions{Check: true, Concurrent: true, MCP: true, Union: true, Install: true, Verifier: true}
	spent, err := askOrDefault(runCtx(t), strings.NewReader(""), io.Discard, false, t.TempDir(), &c,
		tui.Ask{Questions: ask}, organ.Client{})
	if err != nil || len(spent) != 0 {
		t.Fatalf("spent %v, %v", spent, err)
	}
	if c.Union || c.InstallBeads || c.Verifier || c.Unasked != ask {
		t.Errorf("choice %+v", c)
	}
}

func TestTakePrefix(t *testing.T) {
	t.Parallel()
	given := func(v string) project.Given[string] { return project.Given[string]{Value: v, Given: true} }
	held := project.Choice{Prefix: "old", PrefixWas: "old", PrefixRefusal: "the last run left workers on old-1"}
	for _, tc := range []struct {
		name    string
		flag    project.Given[string]
		c       project.Choice
		problem string
		prefix  string
		rename  bool
	}{
		{"not given", project.Given[string]{}, project.Choice{Prefix: "old", PrefixWas: "old"}, "", "old", false},
		{"the same, as typed", given(" old "), project.Choice{Prefix: "old", PrefixWas: "old"}, "", "old", false},
		{"a new one", given("new"), project.Choice{Prefix: "old", PrefixWas: "old"}, "", "new", true},
		{"for a new Beads, whatever holds the old", given("new"),
			project.Choice{Prefix: "old", PrefixWas: "old", PrefixNew: true, PrefixRefusal: "x"}, "", "new", true},
		{"not a prefix", given("9lives"), project.Choice{Prefix: "old", PrefixWas: "old"},
			"--prefix " + project.PrefixProblem("9lives"), "old", false},
		{"tickets held", given("new"), held, "--prefix: the tickets can't be renamed from old to new while the " +
			"last run left workers on old-1. Nothing was changed.", "old", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := tc.c
			if problem := takePrefix(tc.flag, &c); problem != tc.problem || c.Prefix != tc.prefix ||
				c.RenamePrefix != tc.rename {
				t.Errorf("takePrefix = %q, prefix %q, rename %v", problem, c.Prefix, c.RenamePrefix)
			}
		})
	}
}
