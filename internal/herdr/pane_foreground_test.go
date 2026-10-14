package herdr

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// PaneForeground reads the process group in the foreground of a pane's terminal, and its processes,
// from 'herdr pane process-info'; a pane Herdr doesn't have, or an answer it can't read, is an error.
func TestPaneForeground(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	herdrScript(t, "#!/bin/sh\necho \"$*\" > '"+args+"'\n"+
		`echo '{"id":"cli:pane:process_info","result":{"process_info":{"foreground_process_group_id":40543,`+
		`"foreground_processes":[{"argv0":"caffeinate","name":"caffeinate","pid":40911},`+
		`{"argv0":"claude","name":"2.1.299","pid":40543}],"pane_id":"w2E:p3T","shell_pid":39985},`+
		`"type":"pane_process_info"}}'`+"\n")
	group, pids, err := PaneForeground(context.Background(), "w2E:p3T")
	if group != 40543 || !slices.Equal(pids, []int{40911, 40543}) || err != nil {
		t.Errorf("got group %d, pids %v, %v", group, pids, err)
	}
	if got, _ := os.ReadFile(args); string(got) != "pane process-info --pane w2E:p3T\n" {
		t.Errorf("herdr got %q", got)
	}

	failingHerdr(t, paneNotFoundStderr)
	if _, _, err := PaneForeground(context.Background(), "w2E:p3T"); !HasCode(err, PaneNotFound) {
		t.Errorf("a pane Herdr doesn't have: %v", err)
	}

	for _, out := range []string{`{"result":{"type":"pane_process_info","process_info":{}}}`, "not json"} {
		herdrScript(t, "#!/bin/sh\necho '"+out+"'\n")
		if group, _, err := PaneForeground(context.Background(), "w2E:p3T"); group != 0 || err == nil {
			t.Errorf("output %s: %d %v", out, group, err)
		}
	}
}
