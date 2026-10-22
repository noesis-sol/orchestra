package cmux

import (
	"slices"
	"testing"
)

// A workspace's processes are those its terminals' launches started that cmux's top lists in it now:
// not one that has ended, whose ID may be another's by now, nor another workspace's.
func TestTabProcessesAreTheLaunchedProcessesTopLists(t *testing.T) {
	f := newFakeCmux(t)
	term := terminal(t)
	const ended, other = "00000000-0000-4000-8000-0000000000C2", "00000000-0000-4000-8000-0000000000B1"
	keep(t, term, book{Workspaces: map[string]workspaceEntry{workWS: {Label: "x-1"}}, Surfaces: map[string]surfaceEntry{
		workSF: {Workspace: workWS}, ended: {Workspace: workWS, Split: true}, other: {Workspace: userWS}}})
	launchedPID(t, term, workSF, 4242)
	launchedPID(t, term, ended, 4243)
	launchedPID(t, term, other, 4244)
	f.answer("top", topJSON(workSF, 4242, 4244))
	pids, err := term.TabProcesses(bg, workWS)
	if err != nil || !slices.Equal(pids, []int{4242}) {
		t.Errorf("got %v %v, want [4242]", pids, err)
	}
	if got := cmdOf(f.call("top")); !slices.Equal(got, []string{"top", "--processes", "--workspace", workWS}) {
		t.Errorf("top got %q", got)
	}
}

// A workspace none of whose terminals launched anything has no processes, and cmux isn't asked.
func TestTabProcessesOfNoLaunchAsksNothing(t *testing.T) {
	f := newFakeCmux(t)
	term := terminal(t)
	keep(t, term, book{Surfaces: map[string]surfaceEntry{workSF: {Workspace: workWS}}})
	if pids, err := term.TabProcesses(bg, workWS); err != nil || len(pids) != 0 {
		t.Errorf("got %v %v, want none", pids, err)
	}
	if calls := f.calls(); len(calls) != 0 {
		t.Errorf("cmux was asked %q", calls)
	}
}
