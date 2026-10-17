package cmux

import "testing"

// AgentTab is the workspace of the terminal the book has the agent so named in; none when no
// terminal holds it.
func TestAgentTab(t *testing.T) {
	newFakeCmux(t)
	term := terminal(t)
	keep(t, term, book{Workspaces: map[string]workspaceEntry{workWS: {Label: "x-1"}},
		Surfaces: map[string]surfaceEntry{workSF: {Workspace: workWS, Agent: "x-1", Kind: "claude"},
			splitSF: {Workspace: ownWS, Agent: "x-2"}}})
	if tab, err := term.AgentTab(bg, "x-1"); tab != workWS || err != nil {
		t.Errorf("x-1: %q %v", tab, err)
	}
	for _, name := range []string{"x-2", "x-3"} {
		if tab, err := term.AgentTab(bg, name); tab != "" || err != nil {
			t.Errorf("%s, launched nowhere or not there: %q %v", name, tab, err)
		}
	}
}
