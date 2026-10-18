package main

import (
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// Each terminal is a row of terminalKinds: its name, as settings.json and --terminal take it, how the
// log names it, and whether its workers report through hooks; "" is Herdr's, where workers ran before
// there was a choice.
func TestTerminalNamed(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want string
		label      dispatch.TerminalLabel
		hookDriven bool
	}{
		{project.TerminalHerdr, project.TerminalHerdr, dispatch.TerminalLabel{Name: "Herdr", Tabs: "workspace"}, false},
		{"", project.TerminalHerdr, dispatch.TerminalLabel{Name: "Herdr", Tabs: "workspace"}, false},
		{project.TerminalTmux, project.TerminalTmux, dispatch.TerminalLabel{Name: "tmux", Tabs: "session"}, true},
		{project.TerminalCmux, project.TerminalCmux,
			dispatch.TerminalLabel{Name: "cmux", Tabs: "workspaces", Where: "one per ticket"}, true},
	} {
		k := terminalNamed(tc.name)
		if k.name != tc.want || k.label != tc.label || k.hookDriven != tc.hookDriven {
			t.Errorf("%q: %q, label %+v, hook-driven %v; want %q, %+v, %v", tc.name, k.name, k.label, k.hookDriven,
				tc.want, tc.label, tc.hookDriven)
		}
		if got := terminalLabels()[k.name]; got != k.label {
			t.Errorf("%q: the run's labels have %+v, want %+v", tc.name, got, k.label)
		}
	}
}

// --terminal and settings.json take the terminals, in the table's order, and auto.
func TestTerminalChoices(t *testing.T) {
	t.Parallel()
	want := []string{project.TerminalHerdr, project.TerminalTmux, project.TerminalCmux, project.TerminalAuto}
	if got := terminalChoices(); !slices.Equal(got, want) {
		t.Errorf("choices %q, want %q", got, want)
	}
}

// A hook-driven terminal refuses workers whose sessions report through no hooks, naming the terminal,
// the agent and those that do, as it always has.
func TestHookWorkers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ terminal, agent, want string }{
		{"tmux", "codex", "Workers in tmux must be Claude Code (got --agent codex, or AGENT_KIND): only Claude " +
			"workers report through hooks, which tell orchestra what they are doing. Run from a Herdr pane for other agents."},
		{"cmux", "aider", "Workers in cmux must be Claude Code (got --agent aider, or AGENT_KIND): only Claude " +
			"workers report through hooks, which tell orchestra what they are doing. Run from a Herdr pane for other agents."},
	} {
		if got := hookWorkers(tc.terminal, tc.agent); got != tc.want {
			t.Errorf("%s, %s:\n%s\nwant\n%s", tc.terminal, tc.agent, got, tc.want)
		}
	}
}
