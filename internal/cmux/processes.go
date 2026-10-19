package cmux

import (
	"context"
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
