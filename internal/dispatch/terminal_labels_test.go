package dispatch

import (
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// terminalLabels are the labels a run is given for its terminals (cmd/orchestra's terminalKinds).
var terminalLabels = map[string]TerminalLabel{
	project.TerminalHerdr: {Name: "Herdr", Tabs: "workspace"},
	project.TerminalTmux:  {Name: "tmux", Tabs: "session"},
	project.TerminalCmux:  {Name: "cmux", Tabs: "workspaces", Where: "one per ticket"},
}

// A terminal is named as the run's labels have it, "" as Herdr, where workers ran before there was a
// choice; one they lack by its name, its tabs in a workspace.
func TestTerminalLabel(t *testing.T) {
	for _, tc := range []struct {
		labels   map[string]TerminalLabel
		terminal string
		want     TerminalLabel
	}{
		{terminalLabels, project.TerminalTmux, TerminalLabel{Name: "tmux", Tabs: "session"}},
		{terminalLabels, project.TerminalHerdr, TerminalLabel{Name: "Herdr", Tabs: "workspace"}},
		{terminalLabels, "", TerminalLabel{Name: "Herdr", Tabs: "workspace"}},
		{terminalLabels, "screen", TerminalLabel{Name: "screen", Tabs: "workspace"}},
		{nil, project.TerminalCmux, TerminalLabel{Name: "cmux", Tabs: "workspace"}},
	} {
		if got := (Config{TerminalLabels: tc.labels}).terminalLabel(tc.terminal); got != tc.want {
			t.Errorf("%q with labels %v: %+v, want %+v", tc.terminal, tc.labels != nil, got, tc.want)
		}
	}
}
