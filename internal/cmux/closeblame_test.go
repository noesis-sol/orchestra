package cmux

import (
	"testing"
	"time"
)

// From 0.65.0 cmux answers a forced close before what it ends is gone, and orchestra lists what is
// left until it is, for up to settle. A workspace or terminal the maintainer closes in cmux in that
// time, or another orchestra run closes, is not one orchestra's close took with it: the close
// succeeds, and the book drops what it closed.
func TestCloseBlamesOnlyWhatWentByTheAnswer(t *testing.T) {
	f := newFakeCmux(t)
	settle = 5 * time.Second // newFakeCmux's cleanup puts it back
	term := bookOf(t)
	open := treeJSON(ws{ownWS, "orchestra", []string{ownSF}}, ws{userWS, "mine", nil}, ws{workWS, "x-1", []string{workSF}})
	f.answerNth("tree", 1, open)
	f.answerNth("tree", 2, open)
	f.answer("tree", treeJSON(ws{ownWS, "orchestra", []string{ownSF}}))
	if err := term.CloseTab(bg, workWS); err != nil {
		t.Errorf("a workspace the maintainer closed while cmux closed orchestra's: %v", err)
	}
	if b, _ := term.view(); len(b.Workspaces) != 0 || len(b.Surfaces) != 0 {
		t.Errorf("the book still has %+v %+v", b.Workspaces, b.Surfaces)
	}

	f = newFakeCmux(t)
	settle = 5 * time.Second
	keep(t, term, book{Surfaces: map[string]surfaceEntry{splitSF: {Workspace: ownWS, Split: true}}})
	open = treeJSON(ws{ownWS, "orchestra", []string{ownSF, splitSF, workSF}})
	f.answerNth("tree", 1, open)
	f.answerNth("tree", 2, open)
	f.answer("tree", treeJSON(ws{ownWS, "orchestra", []string{ownSF}}))
	if err := term.ClosePane(bg, splitSF); err != nil {
		t.Errorf("a terminal the maintainer closed while cmux closed orchestra's: %v", err)
	}
	if b, _ := term.view(); len(b.Surfaces) != 0 {
		t.Errorf("the book still has %+v", b.Surfaces)
	}
}
