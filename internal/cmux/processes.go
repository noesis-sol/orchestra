package cmux

import (
	"context"
	"encoding/json"
	"slices"
)

// PaneProcesses returns the process at the root of what the surface runs: the one orchestra's last
// launch there started, which wrote its ID (see launch). cmux says nothing of a surface it didn't
// launch into, or one whose launch wrote no ID yet.
func (t Terminal) PaneProcesses(_ context.Context, surface string) ([]int, error) {
	pid, _, ok := t.launched(surface)
	if !ok {
		return nil, missing("top", surface, "process launched in the terminal")
	}
	return []int{pid}, nil
}

// TabProcesses returns the process at the root of what each terminal orchestra opened in the
// workspace runs, as PaneProcesses does, but only those cmux's top lists in the workspace now: a
// launch's process ID may by now name another process, once what it started has ended.
func (t Terminal) TabProcesses(ctx context.Context, workspace string) ([]int, error) {
	b, err := t.view()
	if err != nil {
		return nil, err
	}
	var launched []int
	for surface, e := range b.Surfaces {
		if pid, _, ok := t.launched(surface); ok && sameID(e.Workspace, workspace) {
			launched = append(launched, pid)
		}
	}
	if len(launched) == 0 {
		return nil, nil
	}
	out, err := t.run(ctx, "top", workspace, true, "--processes", "--workspace", workspace)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		return nil, odd("top", workspace, out)
	}
	var pids []int
	for _, pid := range launched {
		if hasPID(v, pid) {
			pids = append(pids, pid)
		}
	}
	slices.Sort(pids)
	return pids, nil
}
