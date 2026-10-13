package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"regexp"

	"github.com/noesis-sol/orchestra/internal/cmux"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/herdr"
	"github.com/noesis-sol/orchestra/internal/project"
	"github.com/noesis-sol/orchestra/internal/tmux"
)

// Workers run in Herdr, tmux or cmux: the project's "terminal" setting, --terminal or
// ORCHESTRA_TERMINAL chooses, or, left to auto, orchestra picks the one it runs in (Herdr in a Herdr
// pane, tmux in tmux, cmux in a cmux terminal), else tmux when tmux is installed. Under tmux the
// workers' windows open in orchestra's own session when it runs in tmux, else in a detached session
// of their own that the maintainer attaches to. Under cmux each worker has a workspace of its own,
// in orchestra's window.

// noTerminal is the problem of a run left to choose its terminal that finds none.
const noTerminal = "No terminal for the workers: run orchestra from a Herdr pane or a cmux terminal, or install " +
	"tmux (brew install tmux, apt install tmux) to run it anywhere."

// pickTerminal chooses the terminal for choice (project.TerminalHerdr, TerminalTmux, TerminalCmux or
// TerminalAuto): auto is the terminal orchestra runs in, the innermost first (Herdr in a Herdr pane,
// tmux in tmux, cmux in a cmux terminal), else tmux when has finds it, else none, and the problem
// says so.
func pickTerminal(choice string, getenv func(string) string, has func(cmd string) bool) (string, string) {
	if choice != project.TerminalAuto {
		return choice, ""
	}
	switch {
	case getenv("HERDR_ENV") == "1":
		return project.TerminalHerdr, ""
	case getenv("TMUX") != "" && has("tmux"):
		return project.TerminalTmux, ""
	case getenv("CMUX_WORKSPACE_ID") != "":
		return project.TerminalCmux, ""
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
	switch t {
	case project.TerminalTmux:
		return checkTmux(ctx, c, getenv)
	case project.TerminalCmux:
		return checkCmux(ctx, c, getenv)
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

// checkCmux finds the cmux terminal orchestra runs in: each worker's workspace opens in its window,
// and the interview splits its surface. cmux answers only processes started in its terminals, so
// outside one it can't be used. As under tmux, workers must be Claude Code, whose hooks say what
// they are doing, and the probe's worker reports through hooks too.
func checkCmux(ctx context.Context, c *options, getenv func(string) string) []string {
	var problems []string
	if !c.ClaudeWorkers() {
		problems = append(problems, "Workers in cmux must be Claude Code (got -agent "+c.AgentKind+", or AGENT_KIND): "+
			"only Claude workers report through hooks, which tell orchestra what they are doing. "+
			"Run from a Herdr pane for other agents.")
	}
	if c.Workspace != "" {
		problems = append(problems, "--workspace has no use under cmux, where each worker has a workspace of its own: "+
			"leave it out.")
	}
	c.ProbeHooks = true
	c.pane, c.cmuxWorkspace, c.inSight = getenv("CMUX_SURFACE_ID"), getenv("CMUX_WORKSPACE_ID"), true
	switch {
	case cmux.Bin() == "":
		problems = append(problems, "Required command not found: cmux (the workers' terminal). Install cmux "+
			"(https://cmux.com), or choose another: orchestra --terminal tmux")
	case c.cmuxWorkspace == "":
		problems = append(problems, "Not running inside a cmux terminal (CMUX_WORKSPACE_ID is not set), and cmux "+
			"answers only programs started in its terminals. Run orchestra from a cmux terminal, "+
			"or choose another: orchestra --terminal tmux")
	default:
		if err := (cmux.Terminal{}).Ping(ctx); err != nil {
			problems = append(problems, "cmux doesn't answer ("+dispatch.FirstLine(err.Error())+"). "+
				"Run orchestra from a cmux terminal, or choose another: orchestra --terminal tmux")
		}
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
	switch c.Terminal {
	case project.TerminalTmux:
		return tmuxSession{repo: c.Repo}
	case project.TerminalCmux:
		return cmux.Terminal{Book: filepath.Join(c.Repo, project.RunPath(cmux.BookName)), Own: c.cmuxWorkspace}
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
