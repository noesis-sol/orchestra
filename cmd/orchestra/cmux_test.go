package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/cmux"
	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
)

// Left to auto, a run in a cmux terminal picks cmux, before tmux, unless it runs in a Herdr pane or
// in tmux inside cmux, the terminal it is in.
func TestPickTerminalInCmux(t *testing.T) {
	for _, tc := range []struct {
		name, choice string
		env          map[string]string
		want         string
	}{
		{"auto in cmux", "auto", map[string]string{"CMUX_WORKSPACE_ID": "W"}, "cmux"},
		{"auto in a Herdr pane in cmux", "auto", map[string]string{"CMUX_WORKSPACE_ID": "W", "HERDR_ENV": "1"}, "herdr"},
		{"auto in tmux in cmux", "auto", map[string]string{"CMUX_WORKSPACE_ID": "W", "TMUX": "/tmp/t,1,0"}, "tmux"},
		{"auto outside cmux", "auto", nil, "tmux"},
		{"cmux chosen outside cmux", "cmux", nil, "cmux"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, problem := pickTerminal(tc.choice, func(k string) string { return tc.env[k] },
				func(string) bool { return true }, func(terminalKind) bool { return true })
			if got != tc.want || problem != "" {
				t.Errorf("got %q, problem %q; want %q", got, problem, tc.want)
			}
		})
	}
}

// fakeCmux puts a cmux first on PATH that answers ping as the script says.
func fakeCmux(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	faketool.Write(t, dir, "cmux", script)
	onPath(t, dir)
}

// In a cmux terminal, auto picks cmux: orchestra's surface is its pane, its workspace is kept from
// being closed, the tabs are in sight, and the probe reports through hooks. The book of workspaces
// is in the main checkout's .orchestra/run/.
func TestConfigCmuxInACmuxTerminal(t *testing.T) {
	configFixture(t, "")
	fakeCmux(t, "exit 0")
	setenv(t, "HERDR_ENV", "")
	setenv(t, "CMUX_WORKSPACE_ID", "0F6C3A9E-1B2C-4D5E-8F90-A1B2C3D4E5F6")
	setenv(t, "CMUX_SURFACE_ID", "7A1B2C3D-4E5F-4061-9728-394A5B6C7D8E")
	c, p := loadWith(t)
	if len(p) > 0 {
		t.Fatalf("problems: %v", p)
	}
	if c.Terminal != project.TerminalCmux || c.Workspace != "" || c.where.pane != "7A1B2C3D-4E5F-4061-9728-394A5B6C7D8E" ||
		c.where.cmuxWorkspace != "0F6C3A9E-1B2C-4D5E-8F90-A1B2C3D4E5F6" || !c.where.inSight || c.Watch != "" || !c.ProbeHooks {
		t.Errorf("terminal %q, workspace %q, pane %q, own workspace %q, in sight %v, watch %q, probe hooks %v",
			c.Terminal, c.Workspace, c.where.pane, c.where.cmuxWorkspace, c.where.inSight, c.Watch, c.ProbeHooks)
	}
	term, ok := terminalOf(c).(cmux.Terminal)
	if !ok || term.Own != c.where.cmuxWorkspace || term.Book != filepath.Join(c.Repo, ".orchestra", "run", "cmux.json") {
		t.Errorf("the terminal is %#v", terminalOf(c))
	}
}

// cmux chosen outside a cmux terminal is a setup problem: cmux answers only programs started in its
// terminals. So is a cmux that doesn't answer, --workspace, which has no use there, and workers
// other than Claude's.
func TestConfigCmuxProblems(t *testing.T) {
	configFixture(t, `{"terminal": "cmux"}`)
	fakeCmux(t, "echo 'Error: Socket not found at /nowhere/cmux.sock' >&2; exit 1")
	_, p := loadWith(t)
	if len(p) != 1 || !strings.Contains(p[0], "Not running inside a cmux terminal") {
		t.Errorf("outside cmux: problems %v", p)
	}
	setenv(t, "CMUX_WORKSPACE_ID", "0F6C3A9E-1B2C-4D5E-8F90-A1B2C3D4E5F6")
	_, p = loadWith(t)
	if len(p) != 1 || !strings.Contains(p[0], "cmux doesn't answer") || !strings.Contains(p[0], "Socket not found") {
		t.Errorf("cmux not answering: problems %v", p)
	}
	fakeCmux(t, "exit 0")
	_, p = loadWith(t, "--workspace", "w2", "--agent", "codex")
	if len(p) != 2 || !strings.Contains(p[0], "Workers in cmux must be Claude Code (got --agent codex") ||
		!strings.Contains(p[1], "--workspace has no use under cmux") {
		t.Errorf("--workspace and codex: problems %v", p)
	}
}
