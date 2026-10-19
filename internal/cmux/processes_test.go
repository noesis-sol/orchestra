package cmux

import (
	"context"
	"slices"
	"testing"
)

// A surface's process is the one its launch started, by the ID it wrote; before it wrote one cmux
// can't say.
func TestPaneProcessesIsTheLaunchedProcess(t *testing.T) {
	term := terminal(t)
	if pids, err := term.PaneProcesses(context.Background(), workSF); err == nil {
		t.Errorf("no launch yet gave %v, want an error", pids)
	}
	launchedPID(t, term, workSF, 4242)
	if pids, err := term.PaneProcesses(context.Background(), workSF); err != nil || !slices.Equal(pids, []int{4242}) {
		t.Errorf("got %v %v, want [4242]", pids, err)
	}
}
