package cmux

import (
	"context"
	"slices"
	"testing"
)

var _ interface {
	SplitPaneAside(ctx context.Context, pane, cwd string) (string, error)
	FocusPane(ctx context.Context, pane string) error
} = Terminal{}

// SplitPaneAside splits as SplitPane does without the focus (--focus false); FocusPane focuses the
// terminal in its workspace (focus-panel).
func TestSplitPaneAsideAndFocusPane(t *testing.T) {
	f := newFakeCmux(t)
	term := terminal(t)
	f.answer("tree", treeJSON(ws{ownWS, "orchestra", []string{ownSF}}))
	f.answer("new-split", `{"pane_id": "33333333-3333-4333-8333-333333333333", "surface_id": "`+splitSF+
		`", "type": "terminal", "workspace_id": "`+ownWS+`"}`)
	split, err := term.SplitPaneAside(bg, ownSF, "/repo")
	if err != nil || split != splitSF {
		t.Fatalf("got %q %v", split, err)
	}
	if got := f.call("new-split"); !slices.Equal(got, []string{"--id-format", "uuids", "--json", "new-split", "right",
		"--workspace", ownWS, "--surface", ownSF, "--focus", "false"}) {
		t.Errorf("new-split got %q", got)
	}

	f = newFakeCmux(t)
	f.answer("tree", treeJSON(ws{ownWS, "orchestra", []string{ownSF, splitSF}}))
	if err := term.FocusPane(bg, splitSF); err != nil {
		t.Fatal(err)
	}
	if got := cmdOf(f.call("focus-panel")); !slices.Equal(got, []string{"focus-panel", "--workspace", ownWS,
		"--panel", splitSF}) {
		t.Errorf("focus-panel got %q", got)
	}
	if err := term.FocusPane(bg, "surface:2"); !Missing(err) {
		t.Errorf("a short ref: %v", err)
	}
}
