package tmux

import "testing"

// AgentTab is the window of the pane holding the agent so named in the repository's panes; none
// when no pane does, and tmux's error when it fails.
func TestAgentTab(t *testing.T) {
	f := newFakeTmux(t)
	f.listPanes(paneLine{id: "%5", window: "@3", agent: "x-1", kind: "claude"},
		paneLine{id: "%6", window: "@4", agent: "x-2", kind: "claude", repo: "/elsewhere"})
	term := Terminal{Repo: "/repo"}
	if tab, err := term.AgentTab(bg, "x-1"); tab != "@3" || err != nil {
		t.Errorf("x-1: %q %v", tab, err)
	}
	for _, name := range []string{"x-2", "x-3"} {
		if tab, err := term.AgentTab(bg, name); tab != "" || err != nil {
			t.Errorf("%s, not the repository's or not there: %q %v", name, tab, err)
		}
	}
	f.serve()
	f.fail("list-panes", "lost server")
	if tab, err := term.AgentTab(bg, "x-1"); tab != "" || err == nil {
		t.Errorf("tmux failing: %q %v", tab, err)
	}
}
