package tmux

import (
	"slices"
	"testing"
)

// SplitPaneAside splits as SplitPane does, detached (-d), so the focus stays where it is; FocusPane
// selects the pane.
func TestSplitPaneAsideAndFocusPane(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("split-window", "%12\n")
	pane, err := Terminal{}.SplitPaneAside(bg, "%2", "/repo")
	if err != nil || pane != "%12" {
		t.Fatalf("got %q %v", pane, err)
	}
	want := []string{"split-window", "-h", "-d", "-t", "%2", "-c", "/repo", "-P", "-F", "#{pane_id}"}
	if got := f.call("split-window"); !slices.Equal(got, want) {
		t.Errorf("split-window got %q", got)
	}

	if err := (Terminal{}).FocusPane(bg, "%12"); err != nil {
		t.Fatal(err)
	}
	if got := f.call("select-pane"); !slices.Equal(got, []string{"select-pane", "-t", "%12"}) {
		t.Errorf("select-pane got %q", got)
	}
	if err := (Terminal{}).FocusPane(bg, "12"); !Missing(err) {
		t.Errorf("no pane ID: %v", err)
	}
}
