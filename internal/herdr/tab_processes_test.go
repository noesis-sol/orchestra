package herdr

import (
	"context"
	"slices"
	"testing"
)

// A tab's processes are those of each of its panes, found among its workspace's.
func TestTabProcesses(t *testing.T) {
	herdrScript(t, "#!/bin/sh\n"+`case "$*" in
"tab get w2E:t2") echo '{"result":{"tab":{"tab_id":"w2E:t2","workspace_id":"w2E"},"type":"tab_info"}}';;
"pane list --workspace w2E") echo '{"result":{"panes":[{"pane_id":"w2E:p1","tab_id":"w2E:t1"},`+
		`{"pane_id":"w2E:p2","tab_id":"w2E:t2"},{"pane_id":"w2E:p3","tab_id":"w2E:t2"}]}}';;
"pane process-info --pane w2E:p2") echo '{"result":{"process_info":{"foreground_process_group_id":20,`+
		`"foreground_processes":[{"pid":20}],"shell_pid":21}}}';;
"pane process-info --pane w2E:p3") echo '{"result":{"process_info":{"foreground_process_group_id":30,`+
		`"foreground_processes":[{"pid":30}],"shell_pid":31}}}';;
*) echo "unexpected: $*" >&2; exit 1;;
esac
`)
	pids, err := Terminal{}.TabProcesses(context.Background(), "w2E:t2")
	if !slices.Equal(pids, []int{21, 20, 31, 30}) || err != nil {
		t.Errorf("got %v, %v; want the shells and foregrounds of the tab's two panes", pids, err)
	}
	failingHerdr(t, `{"error":{"code":"tab_not_found","message":"tab w2E:t2 not found"},"id":"cli:tab:get"}`)
	if _, err := (Terminal{}).TabProcesses(context.Background(), "w2E:t2"); !HasCode(err, TabNotFound) {
		t.Errorf("a tab Herdr doesn't have: %v", err)
	}
}
