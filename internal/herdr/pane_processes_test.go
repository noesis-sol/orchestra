package herdr

import (
	"context"
	"slices"
	"testing"
)

// A pane's processes are the roots of its worker's process tree: the shell Herdr started in it, and
// those in its terminal's foreground.
func TestPaneProcesses(t *testing.T) {
	herdrScript(t, "#!/bin/sh\n"+
		`echo '{"id":"cli:pane:process_info","result":{"process_info":{"foreground_process_group_id":40543,`+
		`"foreground_processes":[{"argv0":"claude","name":"2.1.299","pid":40543}],"pane_id":"w2E:p3T",`+
		`"shell_pid":39985},"type":"pane_process_info"}}'`+"\n")
	pids, err := Terminal{}.PaneProcesses(context.Background(), "w2E:p3T")
	if !slices.Equal(pids, []int{39985, 40543}) || err != nil {
		t.Errorf("got %v, %v", pids, err)
	}
	failingHerdr(t, paneNotFoundStderr)
	if _, err := (Terminal{}).PaneProcesses(context.Background(), "w2E:p3T"); !HasCode(err, PaneNotFound) {
		t.Errorf("a pane Herdr doesn't have: %v", err)
	}
}
