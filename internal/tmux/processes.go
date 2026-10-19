package tmux

import (
	"context"
	"strconv"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
)

// PaneProcesses returns the process at the root of what the pane runs, its pane_pid: the agent
// orchestra launched there, as respawn-pane replaced the pane's process with it.
func (t Terminal) PaneProcesses(ctx context.Context, pane string) ([]int, error) {
	if !isID(pane, '%') {
		return nil, missing("display-message", pane, "pane")
	}
	out, err := t.run(ctx, command.ReadLimit, "display-message", pane, "-p", "-t", pane, "#{pane_pid}")
	if err != nil {
		return nil, err
	}
	// tmux prints an empty line, and succeeds, for a pane it doesn't have.
	if out == "" || out == "\n" {
		return nil, missing("display-message", pane, "pane")
	}
	pid, err := strconv.Atoi(strings.TrimSuffix(out, "\n"))
	if err != nil || pid <= 0 {
		return nil, odd("display-message", pane, out)
	}
	return []int{pid}, nil
}
