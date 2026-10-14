package cmux

import (
	"slices"
	"testing"
	"time"
)

// From 0.65.0 cmux answers new-split and workspace create before it lists what they opened: orchestra
// waits until it does, as a terminal cmux doesn't list reads as closed, and its agent as gone.
func TestOpenWaitsForCmuxToListIt(t *testing.T) {
	f := newFakeCmux(t)
	settle = 5 * time.Second // newFakeCmux's cleanup puts it back
	term := terminal(t)
	before := treeJSON(ws{ownWS, "orchestra", []string{ownSF}})
	f.answerNth("tree", 1, before)
	f.answerNth("tree", 2, before)
	f.answer("tree", treeJSON(ws{ownWS, "orchestra", []string{ownSF, splitSF}}))
	f.answer("new-split", `{"surface_id": "`+splitSF+`", "workspace_id": "`+ownWS+`"}`)
	if split, err := term.SplitPane(bg, ownSF, "/repo"); split != splitSF || err != nil {
		t.Fatalf("got %q %v", split, err)
	}
	if got := slices.DeleteFunc(f.ops(), func(op string) bool { return op != "tree" }); len(got) != 3 {
		t.Errorf("cmux listed its terminals %d times, want 3: before the split, and until it listed it", len(got))
	}

	f = newFakeCmux(t)
	settle = 5 * time.Second
	f.answerNth("tree", 1, before)
	f.answer("tree", treeJSON(ws{ownWS, "orchestra", []string{ownSF}}, ws{workWS, "x-1", []string{workSF}}))
	f.answer("workspace", `{"workspace_id": "`+workWS+`", "surface_id": "`+workSF+`"}`)
	if tab, pane, err := term.CreateTab(bg, "", "/repo", "x-1"); tab != workWS || pane != workSF || err != nil {
		t.Fatalf("got %q %q %v", tab, pane, err)
	}
	if got := slices.DeleteFunc(f.ops(), func(op string) bool { return op != "tree" }); len(got) != 2 {
		t.Errorf("cmux listed its workspaces %d times, want 2: until it listed the new one", len(got))
	}
}
