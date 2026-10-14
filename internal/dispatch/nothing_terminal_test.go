package dispatch

import (
	"context"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// orchestra-74b2.33: a worker the last run left in the other terminal is kept for a run there (see
// loadCarried), so it gives a run in this one nothing to run.
func TestNothingToRunLeavesOutWorkersInTheOtherTerminal(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	b := newFakeBeads()
	b.add("A", "in progress", 1)
	b.set("A", "in_progress")
	g := newFakeGit(c.Base)
	for _, tc := range []struct {
		terminal string
		nothing  bool
	}{
		{project.TerminalTmux, true},
		{"", false}, // Herdr, as this run
	} {
		left := []project.LeftWorker{{Ticket: "A", Tab: "t1", Worktree: "/wt/A", Terminal: tc.terminal, Left: "INTERRUPTED"}}
		if err := project.SaveState(c.Repo, project.RunState{Workers: left}); err != nil {
			t.Fatal(err)
		}
		n, err := CheckNothingToRun(context.Background(), c, b, g, g)
		if err != nil || (n != nil) != tc.nothing {
			t.Errorf("A left in %q: nothing to run %+v (%v), want nothing to run: %v", tc.terminal, n, err, tc.nothing)
		}
	}
}
