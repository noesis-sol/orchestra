package main

import (
	"maps"
	"os"
	"strconv"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
)

// A terminal started from a pane of another copies that one's variables into its panes: left to
// auto, a run picks the terminal it runs in, the innermost, which for Herdr's variables beside tmux's
// or cmux's is Herdr only when Herdr says orchestra runs in the pane they name.
func TestPickTerminalInnermost(t *testing.T) {
	herdrPane := map[string]string{"HERDR_ENV": "1", "HERDR_PANE_ID": "w1:p1"}
	with := func(env map[string]string, more ...string) map[string]string {
		m := maps.Clone(env)
		for i := 0; i < len(more); i += 2 {
			m[more[i]] = more[i+1]
		}
		return m
	}
	for _, tc := range []struct {
		name      string
		env       map[string]string
		tmux, own bool
		want      string
		asked     bool
	}{
		{"a Herdr pane", herdrPane, true, true, "herdr", false},
		{"tmux started from a Herdr pane", with(herdrPane, "TMUX", "/tmp/t,1,0"), true, false, "tmux", true},
		{"a Herdr pane in tmux", with(herdrPane, "TMUX", "/tmp/t,1,0"), true, true, "herdr", true},
		{"tmux started from a Herdr pane, tmux not installed", with(herdrPane, "TMUX", "/tmp/t,1,0"), false, false,
			"herdr", false},
		{"cmux started from a Herdr pane", with(herdrPane, "CMUX_WORKSPACE_ID", "W"), true, false, "cmux", true},
		{"a Herdr pane in cmux", with(herdrPane, "CMUX_WORKSPACE_ID", "W"), true, true, "herdr", true},
		{"tmux in cmux started from a Herdr pane", with(herdrPane, "CMUX_WORKSPACE_ID", "W", "TMUX", "/tmp/t,1,0"),
			true, false, "tmux", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			asked := ""
			got, problem := pickTerminal(project.TerminalAuto, func(k string) string { return tc.env[k] },
				func(cmd string) bool { return cmd != "tmux" || tc.tmux },
				func(pane string) bool { asked = pane; return tc.own })
			if got != tc.want || problem != "" {
				t.Errorf("got %q, problem %q; want %q", got, problem, tc.want)
			}
			if want := map[bool]string{true: "w1:p1"}[tc.asked]; asked != want {
				t.Errorf("asked about Herdr pane %q, want %q", asked, want)
			}
		})
	}
}

// herdrForeground puts a herdr first on PATH that answers process-info with group and pids in the
// foreground of any pane, and fails anything else.
func herdrForeground(t *testing.T, group int, pids ...int) {
	t.Helper()
	list := ""
	for i, pid := range pids {
		if i > 0 {
			list += ","
		}
		list += `{"name":"x","pid":` + strconv.Itoa(pid) + `}`
	}
	dir := t.TempDir()
	faketool.Write(t, dir, "herdr", "#!/bin/sh\n"+
		`[ "$1 $2" = "pane process-info" ] && { echo '{"id":"cli:pane:process_info","result":{"process_info":`+
		`{"foreground_process_group_id":`+strconv.Itoa(group)+`,"foreground_processes":[`+list+`],`+
		`"pane_id":"w1:p1","shell_pid":1},"type":"pane_process_info"}}'; exit 0; }`+
		"\necho 'herdr: not in this test' >&2\nexit 1\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// orchestra runs in a Herdr pane when its process group is in the pane's foreground, or Herdr names
// its process there; not when another program, a tmux client say, is, nor when Herdr doesn't have the
// pane or answer.
func TestOwnHerdrPane(t *testing.T) {
	own := ownHerdrPane(t.Context())
	herdrForeground(t, processGroup(), processGroup())
	if !own("w1:p1") {
		t.Error("orchestra's group in the foreground: not its pane")
	}
	herdrForeground(t, 1<<30, os.Getpid())
	if !own("w1:p1") {
		t.Error("orchestra named in the foreground: not its pane")
	}
	herdrForeground(t, 1<<30, 1<<30)
	if own("w1:p1") {
		t.Error("a tmux client in the foreground: orchestra's pane")
	}
	if own("") {
		t.Error("no pane named: orchestra's")
	}
	dir := t.TempDir()
	faketool.Write(t, dir, "herdr", "#!/bin/sh\n"+
		`echo '{"error":{"code":"pane_not_found","message":"pane w1:p1 not found"},"id":"cli:pane:process_info"}' >&2`+
		"\nexit 1\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if own("w1:p1") {
		t.Error("a pane Herdr doesn't have: orchestra's")
	}
}

// In tmux started from a Herdr pane, which gave tmux's panes its HERDR_ENV and HERDR_PANE_ID, auto
// picks tmux, where orchestra runs: its session and pane. In a Herdr pane inside tmux it picks Herdr.
func TestConfigTmuxStartedFromAHerdrPane(t *testing.T) {
	configFixture(t, "")
	fakeTmux(t)
	t.Setenv("HERDR_PANE_ID", "w7Q:p2")
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Setenv("TMUX_PANE", "%4")
	herdrForeground(t, 1<<30, 1<<30) // the tmux client
	c, p := loadWith(t)
	if len(p) > 0 || c.Terminal != project.TerminalTmux || c.Workspace != "work" || c.where.pane != "%4" || !c.where.inSight {
		t.Errorf("tmux from a Herdr pane: terminal %q, session %q, pane %q, in sight %v, problems %v",
			c.Terminal, c.Workspace, c.where.pane, c.where.inSight, p)
	}

	herdrForeground(t, processGroup(), os.Getpid())
	c, p = loadWith(t)
	if len(p) > 0 || c.Terminal != project.TerminalHerdr || c.Workspace != "w7Q" || c.where.pane != "w7Q:p2" {
		t.Errorf("a Herdr pane in tmux: terminal %q, workspace %q, pane %q, problems %v", c.Terminal, c.Workspace,
			c.where.pane, p)
	}
}
