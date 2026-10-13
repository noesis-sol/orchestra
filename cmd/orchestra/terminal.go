package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/herdr"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tmux"
)

// Workers run in Herdr or in tmux: the project's "terminal" setting, --terminal or ORCHESTRA_TERMINAL
// chooses, or, left to auto, orchestra picks Herdr when it runs in a Herdr pane, else tmux when tmux
// is installed. Under tmux the workers' windows open in orchestra's own session when it runs in
// tmux, else in a detached session of their own that the maintainer attaches to.

// noTerminal is the problem of a run left to choose its terminal that finds neither.
const noTerminal = "No terminal for the workers: run orchestra from a Herdr pane, or install tmux " +
	"(brew install tmux, apt install tmux) to run it anywhere."

// pickTerminal chooses the terminal for choice (project.TerminalHerdr, TerminalTmux or TerminalAuto):
// auto is Herdr in a Herdr pane, else tmux when has finds it, else none, and the problem says so.
func pickTerminal(choice string, getenv func(string) string, has func(cmd string) bool) (string, string) {
	if choice != project.TerminalAuto {
		return choice, ""
	}
	switch {
	case getenv("HERDR_ENV") == "1":
		return project.TerminalHerdr, ""
	case has("tmux"):
		return project.TerminalTmux, ""
	}
	return "", noTerminal
}

// installed reports whether cmd is on the PATH.
func installed(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

// checkTerminal chooses the run's terminal (c.Terminal) as c.terminal, the project's settings and
// where orchestra runs decide, and finds what it needs there: the workspace or session for the
// workers' tabs (c.Workspace), orchestra's own pane (c.pane), whether the tabs are in sight
// (c.inSight) and how to see them if not (c.Watch). It returns the setup problems.
func checkTerminal(ctx context.Context, c *options, getenv func(string) string) []string {
	var settings project.Settings
	if c.Repo != "" {
		settings, _, _ = project.LoadSettings(c.Repo) // resolveProject reports settings it can't read
	}
	choice, err := project.ResolveTerminal(c.terminal, settings)
	if err != nil {
		return []string{err.Error() + "."}
	}
	t, problem := pickTerminal(choice, getenv, installed)
	if problem != "" {
		return []string{problem}
	}
	c.Terminal = t
	if t == project.TerminalTmux {
		return checkTmux(ctx, c, getenv)
	}
	return checkHerdr(ctx, c, getenv)
}

// checkHerdr finds the Herdr pane orchestra runs in and the workspace for the workers' tabs.
func checkHerdr(ctx context.Context, c *options, getenv func(string) string) []string {
	var problems []string
	c.pane, c.inSight = getenv("HERDR_PANE_ID"), true
	if c.Workspace == "" {
		c.Workspace = herdr.CurrentWorkspace(ctx, getenv)
	}
	if c.Workspace == "" && getenv("HERDR_ENV") == "1" {
		problems = append(problems, "Could not tell which Herdr workspace this pane is in. "+
			"Find the ID with 'herdr workspace list', then run: orchestra --workspace <id>")
	}
	if getenv("HERDR_ENV") != "1" {
		problems = append(problems,
			"Not running inside a Herdr pane (HERDR_ENV is not 1). Start 'herdr' and run this from a pane, "+
				"or choose tmux: orchestra --terminal tmux")
	}
	if !installed("herdr") {
		problems = append(problems, "Required command not found: herdr (the workers' terminal)")
	}
	return problems
}

// checkTmux finds the tmux session for the workers' windows: --workspace's, else orchestra's own
// when it runs in tmux, else orchestra-<repository folder>, which the run starts, detached, when
// it opens its first window. The probe's worker reports through hooks, as tmux can't say what a
// worker does.
func checkTmux(ctx context.Context, c *options, getenv func(string) string) []string {
	var problems []string
	if !installed("tmux") {
		problems = append(problems, "Required command not found: tmux (the workers' terminal). "+
			"Install it (brew install tmux, apt install tmux), or run orchestra from a Herdr pane with --terminal herdr.")
	}
	if !c.ClaudeWorkers() {
		problems = append(problems, "Workers in tmux must be Claude Code (got -agent "+c.AgentKind+", or AGENT_KIND): "+
			"only Claude workers report through hooks, which tell orchestra what they are doing. "+
			"Run from a Herdr pane for other agents.")
	}
	c.ProbeHooks = true
	own := ""
	if getenv("TMUX") != "" && getenv("TMUX_PANE") != "" {
		c.pane = getenv("TMUX_PANE")
		name, err := (tmux.Terminal{}).PaneSession(ctx, c.pane)
		switch {
		case err == nil:
			own = name
		case c.Workspace == "":
			problems = append(problems, "Could not tell which tmux session this pane is in ("+err.Error()+"). "+
				"Name it with: orchestra --workspace <session>")
		}
	}
	switch {
	case c.Workspace != "":
	case own != "":
		c.Workspace = own
	case c.Repo != "":
		c.Workspace = tmux.SessionName("orchestra-" + filepath.Base(c.Repo))
	}
	c.inSight = own != "" && c.Workspace == own
	if !c.inSight && c.Workspace != "" {
		how := "tmux attach -t "
		if getenv("TMUX") != "" {
			how = "tmux switch-client -t "
		}
		c.Watch = how + shellWord(c.Workspace)
	}
	return problems
}

// plainWord is a word a shell takes as it is.
var plainWord = regexp.MustCompile(`^[A-Za-z0-9_.,:/@%+=-]+$`)

// shellWord is s as a shell command line gives it: quoted unless it is a plain word.
func shellWord(s string) string {
	if plainWord.MatchString(s) {
		return s
	}
	return command.ShellQuote(s)
}

// workerTerminal is the terminal as a run uses it: the run loop's tabs and agents, the dashboard's
// number keys and the feature interview's pane.
type workerTerminal interface {
	dispatch.Tabs
	dispatch.Starter
	dispatch.Namer
	dispatch.Agents
	interviewPanes
	FocusTab(ctx context.Context, tab string) error
}

// terminalOf is c's terminal.
func terminalOf(c options) workerTerminal {
	if c.Terminal == project.TerminalTmux {
		return tmuxSession{repo: c.Repo}
	}
	return herdr.Terminal{}
}

// tmuxSession is tmux for a run's workers: the session their windows open in is started, detached,
// if tmux doesn't have it, as when orchestra runs outside tmux.
type tmuxSession struct {
	tmux.Terminal
	repo string // where the session's first window starts
}

// CreateTab opens a window as tmux.Terminal does, in session, which it starts first if need be.
func (t tmuxSession) CreateTab(ctx context.Context, session, cwd, label string) (tab, pane string, err error) {
	if err := t.NewSession(ctx, session, t.repo); err != nil {
		return "", "", err
	}
	return t.Terminal.CreateTab(ctx, session, cwd, label)
}
