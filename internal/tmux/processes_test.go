package tmux

import (
	"reflect"
	"slices"
	"testing"
)

// A pane's process is its pane_pid, the root of the worker's process tree.
func TestPaneProcessesIsThePanePID(t *testing.T) {
	f := newFakeTmux(t)
	f.answer("display-message", "4321\n")
	pids, err := Terminal{}.PaneProcesses(bg, "%3")
	if err != nil || !slices.Equal(pids, []int{4321}) {
		t.Fatalf("got %v %v", pids, err)
	}
	if want := []string{"display-message", "-p", "-t", "%3", "#{pane_pid}"}; !reflect.DeepEqual(f.call("display-message"), want) {
		t.Errorf("called %q, want %q", f.call("display-message"), want)
	}
	f.answer("display-message", "\n")
	if _, err := (Terminal{}).PaneProcesses(bg, "%3"); !Missing(err) {
		t.Errorf("an empty answer gave %v, want a missing pane", err)
	}
	f.answer("display-message", "x\n")
	if _, err := (Terminal{}).PaneProcesses(bg, "%3"); err == nil || Missing(err) {
		t.Errorf("an answer that is no process ID gave %v, want an odd answer", err)
	}
}
