package cmux

import (
	"sync"
	"testing"
)

// A second worker's workspace and its terminal.
const (
	work2WS = "00000000-0000-4000-8000-0000000000D0"
	work2SF = "00000000-0000-4000-8000-0000000000D1"
)

// The run loop closes workers' workspaces from several goroutines. Two closes at once each find the
// other's workspace gone from cmux's list after their own close, and must not take it for one cmux
// closed unasked: both succeed, and the book keeps neither.
func TestTwoClosesAtOnce(t *testing.T) {
	f := newFakeCmux(t)
	term := terminal(t)
	keep(t, term, book{Workspaces: map[string]workspaceEntry{workWS: {Label: "x-1"}, work2WS: {Label: "x-2"}},
		Surfaces: map[string]surfaceEntry{workSF: {Workspace: workWS}, work2SF: {Workspace: work2WS}}})
	own := ws{ownWS, "orchestra", []string{ownSF}}
	one, two := ws{workWS, "x-1", []string{workSF}}, ws{work2WS, "x-2", []string{work2SF}}
	f.answer("tree", treeJSON(own, one, two))
	f.write("tree-"+workWS, treeJSON(own, two))
	f.write("tree-"+work2WS, treeJSON(own, one))
	f.write("tree-both", treeJSON(own))
	// cmux's list drops each workspace it closes; a close then waits, up to a quarter of a second, for the
	// other to come in, as two closes at once would.
	f.write("after-close-workspace", `until mkdir "$d/lock" 2>/dev/null; do sleep 0.01; done
if ls "$d" | grep -q '^closed-'; then next=both; else next=$3; fi
cp "$d/tree-$next" "$d/out-tree.new" && mv "$d/out-tree.new" "$d/out-tree"
touch "$d/closed-$3"
rmdir "$d/lock"
i=0
while [ "$(ls "$d" | grep -c '^closed-')" -lt 2 ] && [ $i -lt 5 ]; do sleep 0.05; i=$((i+1)); done
`)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, tab := range []string{workWS, work2WS} {
		wg.Go(func() { errs[i] = term.CloseTab(bg, tab) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("close %d: %v", i+1, err)
		}
	}
	if b, _ := term.view(); len(b.Workspaces) != 0 || len(b.Surfaces) != 0 {
		t.Errorf("the book still has %+v %+v", b.Workspaces, b.Surfaces)
	}
}
