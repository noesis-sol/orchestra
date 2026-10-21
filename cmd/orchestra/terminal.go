package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/noesis-sol/orchestra/internal/cmux"
	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/herdr"
	"github.com/noesis-sol/orchestra/internal/hookterm"
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

// terminalKind is a terminal workers run in: its adapter is a package of its own (internal/herdr,
// internal/tmux, internal/cmux), and a row of terminalKinds tells a run how to choose it, check it and
// open it, and how the log names it.
type terminalKind struct {
	// name is the terminal's, as settings.json's "terminal", --terminal and the run's state file have it.
	name string
	// label is how the log names it and the place of its workers' tabs.
	label dispatch.TerminalLabel
	// inside reports whether orchestra runs in one of its panes, as its variables in getenv say (has
	// finds a command on the PATH).
	inside func(getenv func(string) string, has func(cmd string) bool) bool
	// depth is how deep the terminal's panes are where terminals run one in another: one started from a
	// pane of another copies that one's variables into its own panes, so where several terminals'
	// variables are set orchestra runs in the deepest, unless ownPane says it runs in another's pane.
	depth int
	// ownPane reports whether orchestra runs in the terminal's pane that getenv names, where another
	// terminal's variables are set too; nil for a terminal that can't tell.
	ownPane func(ctx context.Context, getenv func(string) string) bool
	// fallback has a run left to auto outside every terminal pick this one when its command is installed.
	fallback bool
	// check finds what a run needs in the terminal, in c (see checkTerminal), and returns the problems.
	check func(ctx context.Context, c *options, getenv func(string) string) []string
	// hookDriven is a terminal that can't read what an agent is doing from its screen: its workers, the
	// probe's and the interview's claude report through hooks (hookterm), so only an agent whose sessions
	// do (hookterm.Reports) runs in it.
	hookDriven bool
	// open is the terminal as the run c uses it.
	open func(c options) workerTerminal
}

// terminalKinds are the terminals workers run in, in the order a problem lists them: adding one is its
// package and a row here.
var terminalKinds = []terminalKind{
	{
		name:  project.TerminalHerdr,
		label: dispatch.TerminalLabel{Name: "Herdr", Tabs: "workspace"},
		inside: func(getenv func(string) string, _ func(string) bool) bool {
			return getenv("HERDR_ENV") == "1"
		},
		depth:   0,
		ownPane: ownHerdrPane,
		check:   checkHerdr,
		open:    func(options) workerTerminal { return herdr.Terminal{} },
	},
	{
		name:  project.TerminalTmux,
		label: dispatch.TerminalLabel{Name: "tmux", Tabs: "session"},
		inside: func(getenv func(string) string, has func(string) bool) bool {
			return getenv("TMUX") != "" && has("tmux")
		},
		depth:      2,
		fallback:   true,
		check:      checkTmux,
		hookDriven: true,
		open:       func(c options) workerTerminal { return tmuxSession{tmux.Terminal{Repo: c.Repo}} },
	},
	{
		name:  project.TerminalCmux,
		label: dispatch.TerminalLabel{Name: "cmux", Tabs: "workspaces", Where: "one per ticket"},
		inside: func(getenv func(string) string, _ func(string) bool) bool {
			return getenv("CMUX_WORKSPACE_ID") != ""
		},
		depth:      1,
		check:      checkCmux,
		hookDriven: true,
		open: func(c options) workerTerminal {
			return cmux.Terminal{Book: filepath.Join(c.Repo, project.RunPath(cmux.BookName)), Own: c.where.cmuxWorkspace}
		},
	},
}

// terminalNamed is the terminal named name in terminalKinds; "" is Herdr (project.SameTerminal).
func terminalNamed(name string) terminalKind {
	for _, k := range terminalKinds {
		if project.SameTerminal(k.name, name) {
			return k
		}
	}
	return terminalKinds[0] // checkTerminal takes no other name
}

// terminalChoices are the terminals' names and auto, as --terminal and settings.json take them.
func terminalChoices() []string {
	var names []string
	for _, k := range terminalKinds {
		names = append(names, k.name)
	}
	return append(names, project.TerminalAuto)
}

// terminalLabels are how the log names each terminal (dispatch.Config.TerminalLabels).
func terminalLabels() map[string]dispatch.TerminalLabel {
	labels := make(map[string]dispatch.TerminalLabel, len(terminalKinds))
	for _, k := range terminalKinds {
		labels[k.name] = k.label
	}
	return labels
}

// pickTerminal chooses the terminal for choice (a terminal's name or project.TerminalAuto): auto is the
// terminal orchestra runs in, whose variables getenv has (terminalKind.inside), the innermost first,
// else the fallback terminal when has finds it, else none, and the problem says so. A terminal
// started from a pane of another copies that one's variables into its own panes, so where several
// terminals' are set, orchestra runs in the deepest, unless ownPane says it runs in the pane of one
// that can tell (Herdr: its HERDR_PANE_ID), where a terminal started from it would run instead.
func pickTerminal(choice string, getenv func(string) string, has func(cmd string) bool,
	ownPane func(k terminalKind) bool) (string, string) {
	if choice != project.TerminalAuto {
		return choice, ""
	}
	var in []terminalKind
	for _, k := range terminalKinds {
		if k.inside(getenv, has) {
			in = append(in, k)
		}
	}
	if len(in) == 1 {
		return in[0].name, ""
	}
	for _, k := range in {
		if k.ownPane != nil && ownPane(k) {
			return k.name, ""
		}
	}
	if len(in) > 0 {
		return slices.MaxFunc(in, func(a, b terminalKind) int { return a.depth - b.depth }).name, ""
	}
	for _, k := range terminalKinds {
		if k.fallback && has(k.name) {
			return k.name, ""
		}
	}
	return "", noTerminal
}

// ownHerdrPane reports whether orchestra runs in the Herdr pane HERDR_PANE_ID names: whether its process
// group is the one in the foreground of the pane's terminal, where a terminal started from the pane, a
// tmux client say, would be instead. A pane Herdr doesn't have, or a Herdr that doesn't answer, is not
// orchestra's.
func ownHerdrPane(ctx context.Context, getenv func(string) string) bool {
	pane := getenv("HERDR_PANE_ID")
	if pane == "" {
		return false
	}
	group, pids, err := herdr.PaneForeground(ctx, pane)
	return err == nil && (group == processGroup() || slices.Contains(pids, os.Getpid()))
}

// installed reports whether cmd is on the PATH of the commands run with ctx (command.LookPath).
func installed(ctx context.Context, cmd string) bool {
	_, err := command.LookPath(ctx, cmd)
	return err == nil
}

// onHost reports whether this machine has cmd: on the PATH or, for cmux, in its app (cmux.Bin), its
// CLI being on the PATH only in cmux's own terminals.
func onHost(ctx context.Context) func(cmd string) bool {
	return func(cmd string) bool {
		if cmd == "cmux" {
			return cmux.Bin(ctx) != ""
		}
		return installed(ctx, cmd)
	}
}

// checkTerminal chooses the run's terminal (c.Terminal) as c.terminalAsked, the project's settings and
// where orchestra runs decide, and finds what it needs there: the workspace or session for the
// workers' tabs (c.Workspace), where orchestra runs (c.where: its own pane, whether the tabs are in
// sight), how to see the tabs if not (c.Watch) and how the log names terminals. A hook-driven
// terminal takes only workers whose sessions report through hooks, and has the probe's report
// through them too. It returns the setup problems.
func checkTerminal(ctx context.Context, c *options, settings project.Settings, getenv func(string) string) []string {
	choice, err := project.ResolveTerminal(c.terminalAsked, settings, terminalChoices())
	if err != nil {
		return []string{err.Error() + "."}
	}
	t, problem := pickTerminal(choice, getenv, func(cmd string) bool { return installed(ctx, cmd) },
		func(k terminalKind) bool { return k.ownPane(ctx, getenv) })
	if problem != "" {
		return []string{problem}
	}
	c.Terminal, c.TerminalLabels = t, terminalLabels()
	k := terminalNamed(t)
	var problems []string
	if k.hookDriven {
		if !hookterm.Reports(c.AgentKind) {
			problems = append(problems, hookWorkers(k.name, c.AgentKind))
		}
		c.ProbeHooks = true
	}
	return append(problems, k.check(ctx, c, getenv)...)
}

// hookWorkers is the problem of a run in the hook-driven terminal whose workers are agent, whose
// sessions don't report through hooks (hookterm.Reports): it names those that do.
func hookWorkers(terminal, agent string) string {
	return "Workers in " + terminal + " must be Claude Code (got --agent " + agent + ", or AGENT_KIND): " +
		"only Claude workers report through hooks, which tell orchestra what they are doing. " +
		"Run from a Herdr pane for other agents."
}

// checkHerdr finds the Herdr pane orchestra runs in and the workspace for the workers' tabs.
func checkHerdr(ctx context.Context, c *options, getenv func(string) string) []string {
	var problems []string
	c.where.pane, c.where.inSight = getenv("HERDR_PANE_ID"), true
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
	if !installed(ctx, "herdr") {
		problems = append(problems, "Required command not found: herdr (the workers' terminal)")
	}
	return problems
}

// checkTmux finds the tmux session for the workers' windows: --workspace's, else orchestra's own
// when it runs in tmux, else orchestra-<repository folder>, which the run starts, detached, when
// it opens its first window.
func checkTmux(ctx context.Context, c *options, getenv func(string) string) []string {
	var problems []string
	if !installed(ctx, "tmux") {
		problems = append(problems, "Required command not found: tmux (the workers' terminal). "+
			"Install it (brew install tmux, apt install tmux), or run orchestra from a Herdr pane with --terminal herdr.")
	} else if v := tmux.TooOld(ctx); v != "" {
		problems = append(problems, v+" is too old: orchestra needs tmux "+tmux.MinVersion+" or later. "+
			"Upgrade it (brew upgrade tmux, apt install tmux), or run orchestra from a Herdr pane with --terminal herdr.")
	}
	own := ""
	if getenv("TMUX") != "" && getenv("TMUX_PANE") != "" {
		c.where.pane = getenv("TMUX_PANE")
		name, err := (tmux.Terminal{}).PaneSession(ctx, c.where.pane)
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
	if strings.Contains(c.Workspace, ":") && c.Workspace != own {
		// tmux splits a target at its first ':', session from window, so a session named foo:bar,
		// which tmux 3.8 keeps, can't be attached to by name: it is foo_bar on every tmux.
		c.Workspace = tmux.SessionName(c.Workspace)
	}
	c.where.inSight = own != "" && c.Workspace == own
	if !c.where.inSight && c.Workspace != "" {
		how := "tmux attach -t "
		if getenv("TMUX") != "" {
			how = "tmux switch-client -t "
		}
		target := c.Workspace
		if tmux.SessionName(target) != target && !tmux.KeepsNames(ctx) {
			target = tmux.SessionName(target) // the session NewSession starts, or finds, before tmux 3.8
		}
		if strings.Contains(target, ".") {
			target += ":" // the session, where tmux would read "my.sess" as window "my", pane "sess"
		}
		c.Watch = how + command.ShellWord(target)
	}
	return problems
}

// checkCmux finds the cmux terminal orchestra runs in: each worker's workspace opens in its window,
// and the interview splits its surface. cmux answers only processes started in its terminals, so
// outside one it can't be used.
func checkCmux(ctx context.Context, c *options, getenv func(string) string) []string {
	var problems []string
	if c.Workspace != "" {
		problems = append(problems, "--workspace has no use under cmux, where each worker has a workspace of its own: "+
			"leave it out.")
	}
	c.where = runsIn{pane: getenv("CMUX_SURFACE_ID"), cmuxWorkspace: getenv("CMUX_WORKSPACE_ID"), inSight: true}
	switch {
	case cmux.Bin(ctx) == "":
		problems = append(problems, "Required command not found: cmux (the workers' terminal). Install cmux "+
			"(https://cmux.com), or choose another: orchestra --terminal tmux")
	case c.where.cmuxWorkspace == "":
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

// workerTerminal is the terminal as a run uses it: the run loop's tabs and agents and their processes, the dashboard's
// number keys, the feature interview's pane and the answer talk's.
type workerTerminal interface {
	dispatch.Tabs
	dispatch.Starter
	dispatch.Namer
	dispatch.Agents
	dispatch.PaneProcesses
	interviewPanes
	FocusTab(ctx context.Context, tab string) error
	SplitPaneAside(ctx context.Context, pane, cwd string) (string, error) // the answer talk's, opened by itself
	FocusPane(ctx context.Context, pane string) error                     // the dashboard's a key, to the answer talk
}

// terminalOf is c's terminal.
func terminalOf(c options) workerTerminal {
	return terminalNamed(c.Terminal).open(c)
}

// tmuxSession is tmux for a run's workers: the session their windows open in is started, detached,
// if tmux doesn't have it, as when orchestra runs outside tmux, its first window in the repository.
type tmuxSession struct {
	tmux.Terminal
}

// CreateTab opens a window as tmux.Terminal does, in session, which it starts first if need be.
func (t tmuxSession) CreateTab(ctx context.Context, session, cwd, label string) (tab, pane string, err error) {
	id, err := t.NewSession(ctx, session, t.Repo)
	if err != nil {
		return "", "", err
	}
	return t.Terminal.CreateTab(ctx, id, cwd, label)
}
