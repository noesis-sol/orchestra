package cmux

import (
	"slices"
	"testing"
	"time"
)

// From 0.65.0 cmux won't close a terminal a process runs in without --force: orchestra closes a
// worker's workspace, and the interview's terminal, with it, to end what runs there. Before 0.65.0
// --force is left out.
func TestCloseForcesFrom065(t *testing.T) {
	for _, c := range []struct {
		version string
		force   []string
	}{
		{version065, []string{"--force"}},
		{version064, nil},
	} {
		f := newFakeCmux(t)
		f.answer("version", c.version)
		term := bookOf(t)
		f.answerNth("tree", 1, treeJSON(ws{ownWS, "orchestra", []string{ownSF}}, ws{workWS, "x-1", []string{workSF}}))
		f.answerNth("tree", 2, treeJSON(ws{ownWS, "orchestra", []string{ownSF}}))
		if err := term.CloseTab(bg, workWS); err != nil {
			t.Fatal(err)
		}
		if got, want := cmdOf(f.call("close-workspace")), append([]string{"close-workspace", "--workspace", workWS},
			c.force...); !slices.Equal(got, want) {
			t.Errorf("%s: close-workspace got %q, want %q", c.version, got, want)
		}

		f = newFakeCmux(t)
		f.answer("version", c.version)
		keep(t, term, book{Surfaces: map[string]surfaceEntry{splitSF: {Workspace: ownWS, Split: true}}})
		f.answerNth("tree", 1, treeJSON(ws{ownWS, "orchestra", []string{ownSF, splitSF}}))
		f.answerNth("tree", 2, treeJSON(ws{ownWS, "orchestra", []string{ownSF}}))
		if err := term.ClosePane(bg, splitSF); err != nil {
			t.Fatal(err)
		}
		if got, want := cmdOf(f.call("close-surface")), append([]string{"close-surface", "--workspace", ownWS,
			"--surface", splitSF}, c.force...); !slices.Equal(got, want) {
			t.Errorf("%s: close-surface got %q, want %q", c.version, got, want)
		}
	}
}

// From 0.65.0 cmux answers a forced close before the terminal is gone: the close waits for cmux's
// list to drop it, rather than call it still open.
func TestCloseWaitsForCmuxToDropIt(t *testing.T) {
	f := newFakeCmux(t)
	settle = 5 * time.Second // newFakeCmux's cleanup puts it back
	f.answer("version", version065)
	term := bookOf(t)
	open := treeJSON(ws{ownWS, "orchestra", []string{ownSF}}, ws{workWS, "x-1", []string{workSF}})
	f.answerNth("tree", 1, open)
	f.answerNth("tree", 2, open)
	f.answerNth("tree", 3, open)
	f.answer("tree", treeJSON(ws{ownWS, "orchestra", []string{ownSF}}))
	if err := term.CloseTab(bg, workWS); err != nil {
		t.Fatal(err)
	}
	if b, _ := term.view(); len(b.Workspaces) != 0 {
		t.Errorf("the book still has %+v", b.Workspaces)
	}
}
