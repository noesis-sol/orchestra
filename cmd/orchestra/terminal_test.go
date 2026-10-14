package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
)

// Left to auto, a run picks Herdr in a Herdr pane, else tmux when it is installed, else neither; a
// terminal chosen is taken as it is, checkTerminal saying when it is missing.
func TestPickTerminal(t *testing.T) {
	for _, tc := range []struct {
		name, choice string
		inHerdr      bool
		installed    []string
		want         string
		problem      bool
	}{
		{"auto in Herdr, both installed", "auto", true, []string{"herdr", "tmux"}, "herdr", false},
		{"auto in Herdr, only tmux installed", "auto", true, []string{"tmux"}, "herdr", false},
		{"auto outside Herdr, both installed", "auto", false, []string{"herdr", "tmux"}, "tmux", false},
		{"auto outside Herdr, only tmux installed", "auto", false, []string{"tmux"}, "tmux", false},
		{"auto outside Herdr, neither installed", "auto", false, nil, "", true},
		{"auto outside Herdr, only herdr installed", "auto", false, []string{"herdr"}, "", true},
		{"herdr chosen outside Herdr", "herdr", false, []string{"tmux"}, "herdr", false},
		{"tmux chosen in Herdr", "tmux", true, []string{"herdr", "tmux"}, "tmux", false},
		{"tmux chosen, not installed", "tmux", false, nil, "tmux", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(k string) string {
				if k == "HERDR_ENV" && tc.inHerdr {
					return "1"
				}
				return ""
			}
			has := func(cmd string) bool { return strings.Contains(strings.Join(tc.installed, " "), cmd) }
			got, problem := pickTerminal(tc.choice, getenv, has, func(string) bool { return true })
			if got != tc.want || (problem != "") != tc.problem {
				t.Errorf("got %q, problem %q; want %q, a problem: %v", got, problem, tc.want, tc.problem)
			}
			if tc.problem && (!strings.Contains(problem, "Herdr pane") || !strings.Contains(problem, "install tmux")) {
				t.Errorf("the problem doesn't say what to do: %s", problem)
			}
		})
	}
}

// fakeTmux puts a tmux first on PATH that names the session of any pane work, and fails anything
// else.
func fakeTmux(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	faketool.Write(t, dir, "tmux", "#!/bin/sh\n"+
		`[ "$1" = display-message ] && { echo work; exit 0; }`+"\necho 'tmux: not in this test' >&2\nexit 1\n")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// withoutTmux leaves on PATH only git and the fake bd and herdr hostTools puts there, so that tmux
// is not found; the test is skipped where git's folder holds a tmux too.
func withoutTmux(t *testing.T) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(git), "tmux")); err == nil {
		t.Skip("tmux is installed beside git")
	}
	var path []string
	for dir := range strings.SplitSeq(os.Getenv("PATH"), string(os.PathListSeparator)) {
		if _, err := os.Stat(filepath.Join(dir, "herdr")); err == nil && strings.HasPrefix(dir, os.TempDir()) {
			path = append(path, dir) // hostTools'
		}
	}
	t.Setenv("PATH", strings.Join(append(path, filepath.Dir(git)), string(os.PathListSeparator)))
}

// Outside tmux, the workers' windows open in a detached session named after the repository, which
// the START block, the dashboard and the end of the run say how to attach to; the number keys can't
// go to them. The project's setting chooses tmux even in a Herdr pane, and the probe reports through
// hooks.
func TestConfigTmuxOutsideTmuxUsesASessionOfItsOwn(t *testing.T) {
	repo := configFixture(t, `{"terminal": "tmux"}`)
	fakeTmux(t)
	c, p := loadWith(t)
	if len(p) > 0 {
		t.Fatalf("problems: %v", p)
	}
	session := "orchestra-" + strings.ReplaceAll(filepath.Base(repo), ".", "_")
	if c.Terminal != project.TerminalTmux || c.Workspace != session || c.Watch != "tmux attach -t "+session ||
		c.inSight || c.pane != "" || !c.ProbeHooks {
		t.Errorf("terminal %q, session %q, watch %q, in sight %v, pane %q, probe hooks %v",
			c.Terminal, c.Workspace, c.Watch, c.inSight, c.pane, c.ProbeHooks)
	}
}

// Inside tmux, the workers' windows open in orchestra's own session, in sight: no attaching. Another
// session named with --workspace is out of sight, and switched to.
func TestConfigTmuxInsideTmuxUsesItsOwnSession(t *testing.T) {
	configFixture(t, "")
	fakeTmux(t)
	t.Setenv("HERDR_ENV", "")
	t.Setenv("TMUX", "/tmp/tmux-501/default,1,0")
	t.Setenv("TMUX_PANE", "%4")
	c, p := loadWith(t)
	if len(p) > 0 {
		t.Fatalf("problems: %v", p)
	}
	if c.Terminal != project.TerminalTmux || c.Workspace != "work" || c.Watch != "" || !c.inSight || c.pane != "%4" {
		t.Errorf("terminal %q, session %q, watch %q, in sight %v, pane %q", c.Terminal, c.Workspace, c.Watch,
			c.inSight, c.pane)
	}
	c, p = loadWith(t, "--workspace", "my workers")
	if len(p) > 0 {
		t.Fatalf("problems: %v", p)
	}
	if c.Workspace != "my workers" || c.Watch != "tmux switch-client -t 'my workers'" || c.inSight {
		t.Errorf("session %q, watch %q, in sight %v", c.Workspace, c.Watch, c.inSight)
	}
}

// --terminal overrides ORCHESTRA_TERMINAL, which overrides the project's setting.
func TestConfigTerminalPrecedence(t *testing.T) {
	configFixture(t, `{"terminal": "tmux"}`)
	fakeTmux(t)
	t.Setenv("ORCHESTRA_TERMINAL", "herdr")
	if c, p := loadWith(t); len(p) > 0 || c.Terminal != project.TerminalHerdr || c.Workspace != "w7Q" || !c.inSight {
		t.Errorf("ORCHESTRA_TERMINAL: terminal %q, workspace %q, problems %v", c.Terminal, c.Workspace, p)
	}
	if c, p := loadWith(t, "--terminal", "tmux"); len(p) > 0 || c.Terminal != project.TerminalTmux {
		t.Errorf("--terminal: terminal %q, problems %v", c.Terminal, p)
	}
	if c, p := loadWith(t, "--terminal", "auto"); len(p) > 0 || c.Terminal != project.TerminalHerdr {
		t.Errorf("--terminal auto in a Herdr pane: terminal %q, problems %v", c.Terminal, p)
	}
	_, p := loadWith(t, "--terminal", "screen")
	if len(p) != 1 || !strings.Contains(p[0], "--terminal") || !strings.Contains(p[0], "herdr, tmux, cmux, auto") {
		t.Errorf("--terminal screen: problems %v", p)
	}
}

func TestConfigRefusesAnUnknownTerminalSetting(t *testing.T) {
	configFixture(t, `{"terminal": "screen"}`)
	_, p := loadWith(t)
	if len(p) != 1 || !strings.Contains(p[0], "terminal must be one of herdr, tmux, cmux, auto (got 'screen')") {
		t.Errorf("problems %v", p)
	}
}

// Under tmux only Claude workers can run: their hooks tell orchestra what they are doing.
func TestConfigTmuxRefusesWorkersOtherThanClaude(t *testing.T) {
	configFixture(t, "")
	fakeTmux(t)
	_, p := loadWith(t, "--terminal", "tmux", "-agent", "codex")
	if len(p) != 1 || !strings.Contains(p[0], "Workers in tmux must be Claude Code (got -agent codex") {
		t.Errorf("problems %v", p)
	}
	if _, p := loadWith(t, "--terminal", "herdr", "-agent", "codex"); len(p) > 0 {
		t.Errorf("Herdr runs any agent: problems %v", p)
	}
}

// tmux chosen and not installed is a setup problem naming it; left to auto outside a Herdr pane,
// with no tmux, the problem says how to get either.
func TestConfigTerminalMissing(t *testing.T) {
	configFixture(t, "")
	withoutTmux(t)
	_, p := loadWith(t, "--terminal", "tmux")
	if len(p) != 1 || !strings.Contains(p[0], "Required command not found: tmux") {
		t.Errorf("tmux chosen: problems %v", p)
	}
	t.Setenv("HERDR_ENV", "")
	_, p = loadWith(t)
	if len(p) != 1 || p[0] != noTerminal {
		t.Errorf("neither: problems %v", p)
	}
}

// Under Herdr, as before: its pane and workspace, in sight.
func TestConfigHerdrInAHerdrPane(t *testing.T) {
	configFixture(t, "")
	fakeTmux(t)
	t.Setenv("HERDR_PANE_ID", "w7Q:p2")
	c, p := loadWith(t)
	if len(p) > 0 || c.Terminal != project.TerminalHerdr || c.Workspace != "w7Q" || c.pane != "w7Q:p2" ||
		!c.inSight || c.Watch != "" || c.ProbeHooks {
		t.Errorf("terminal %q, workspace %q, pane %q, in sight %v, watch %q, probe hooks %v, problems %v",
			c.Terminal, c.Workspace, c.pane, c.inSight, c.Watch, c.ProbeHooks, p)
	}
}
