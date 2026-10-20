package dispatch

import (
	"context"
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// fakeMergeState is what a run knows of the tickets not merged yet, as a test sets it.
type fakeMergeState struct {
	busyIDs  map[string]bool
	leftTabs map[string]string // the tabs workers were left in, by ticket
	unmerged map[string]string
}

func (s fakeMergeState) busy(id string) bool { return s.busyIDs[id] }
func (s fakeMergeState) leftOpenWhy(id string) string {
	if tab, ok := s.leftTabs[id]; ok {
		return leftInTab(tab)
	}
	return ""
}
func (s fakeMergeState) closedWhy(ctx context.Context, id string) string { return s.unmerged[id] }

// TestPendingWhy pins the one rule for whether a ticket blocking another has merged: the holds, a
// worker stopped with nothing done and the deferred tickets freed all ask it.
func TestPendingWhy(t *testing.T) {
	t.Parallel()
	s := fakeMergeState{
		busyIDs:  map[string]bool{"k-run": true, "k-ask": true},
		leftTabs: map[string]string{"k-tab": "tab7"},
		unmerged: map[string]string{"k-left": earlierRun, "k-merging": "merge conflict"},
	}
	for _, tc := range []struct {
		name   string
		id     string
		closed bool
		want   string
	}{
		{"running, not closed yet", "k-run", false, pendingBusy},
		{"running, closed by its worker before it merges", "k-run", true, pendingBusy},
		{"asked, closed in its tab", "k-ask", true, pendingBusy},
		{"set aside, its worker left in its tab", "k-tab", false, "its worker was left in tab tab7"},
		{"set aside, closed in its worker's tab", "k-tab", true, "its worker was left in tab tab7"},
		{"open or deferred", "k-open", false, pendingOpen},
		{"closed, left unmerged by an earlier run", "k-left", true, earlierRun},
		{"closed, not merged in this run", "k-merging", true, "merge conflict"},
		{"closed and merged", "k-done", true, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := pendingWhy(context.Background(), s, tc.id, tc.closed); got != tc.want {
				t.Errorf("pendingWhy(%s, closed %v) = %q, want %q", tc.id, tc.closed, got, tc.want)
			}
		})
	}
}

// TestPendingWhyStates checks that each state the run keeps answers pendingWhy as it knows the
// tickets: the holds from their snapshot, and a run's start from the workers left and the tickets
// labelled unmerged.
func TestPendingWhyStates(t *testing.T) {
	t.Parallel()
	h := holds{running: map[string]bool{"k-run": true}, asked: map[string]bool{"k-ask": true},
		unmerged: func(id string) string { return map[string]string{"k-left": earlierRun}[id] }}
	st := newStartState([]project.LeftWorker{{Ticket: "k-run"}}, []Ticket{{ID: "k-left", Status: StatusClosed}})
	for _, tc := range []struct {
		name  string
		state mergeState
		want  map[string]string
	}{
		{"holds", h, map[string]string{"k-run": pendingBusy, "k-ask": pendingBusy, "k-left": earlierRun, "k-done": ""}},
		{"start", st, map[string]string{"k-run": pendingBusy, "k-ask": "", "k-left": earlierRun, "k-done": ""}},
	} {
		for id, want := range tc.want {
			if got := pendingWhy(context.Background(), tc.state, id, true); got != want {
				t.Errorf("%s: pendingWhy(%s) = %q, want %q", tc.name, id, got, want)
			}
		}
	}
}

// TestUnmergedBlockersStaleLabel pins the maintainer's rule (orchestra-kt3s): a worker stopped with
// nothing done waits only for the blockers the run knows have not merged. One whose UnmergedLabel bd
// couldn't remove once it merged has merged, as the holds and DEFERRED_FREE find it.
func TestUnmergedBlockersStaleLabel(t *testing.T) {
	t.Parallel()
	g := newFakeGit("main") // no branches: each closed blocker's work is on main, as far as git says
	o := New(Config{Base: "main"}, nil, "", Deps{Checkout: g, Worktrees: g, Merger: g})
	o.active = map[string]Status{"k-run": {Ticket: "k-run"}}
	o.askedIDs = map[string]workerSpot{"k-ask": {}}
	o.unmerged = map[string]string{"k-left": earlierRun}
	blocks := func(id string, status TicketStatus, labels ...string) Ticket {
		return Ticket{ID: id, Status: status, DependencyType: "blocks", Labels: labels}
	}
	for _, tc := range []struct {
		name    string
		blocker Ticket
		want    []string
	}{
		{"running", blocks("k-run", StatusInProgress), []string{"k-run"}},
		{"asked, closed in its tab", blocks("k-ask", StatusClosed), []string{"k-ask"}},
		{"open", blocks("k-open", StatusOpen), []string{"k-open"}},
		{"closed, left unmerged", blocks("k-left", StatusClosed, UnmergedLabel), []string{"k-left"}},
		{"closed and merged", blocks("k-done", StatusClosed), nil},
		{"closed and merged, its label stale", blocks("k-stale", StatusClosed, UnmergedLabel), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := Ticket{ID: "k-b", Status: StatusOpen, Dependencies: []Ticket{
				tc.blocker, {ID: "k-x", Status: StatusOpen, DependencyType: "related"},
			}}
			if got := o.unmergedBlockers(context.Background(), info); !slices.Equal(got, tc.want) {
				t.Errorf("unmergedBlockers = %v, want %v", got, tc.want)
			}
		})
	}
}
