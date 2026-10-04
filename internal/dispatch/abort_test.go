package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/git"
)

// noAbort is git whose rebase --abort fails, leaving the worktree mid-rebase.
type noAbort struct{ git.Git }

func (noAbort) AbortRebase(ctx context.Context, worktree string) (string, error) {
	return "", errors.New("git rebase --abort: exit status 128")
}

// A conflict that can't be aborted is still set aside, saying the worktree is mid-rebase.
func TestMergeConflictWhoseAbortFailsSaysSo(t *testing.T) {
	f := newMergeFixture(t, "true")
	f.orch.merger = noAbort{}
	wt := f.ticket(t, "k-1", "shared.txt", "line 1 from the ticket\n")
	f.onMain(t, "shared.txt", "line 1 from main\n")
	if s := f.orch.merge(context.Background(), worker{id: "k-1", br: "wt/k-1", wt: wt, tab: "tab"}); s != nil {
		t.Fatal(s)
	}
	if ev := f.sink.text(); !strings.Contains(ev, "MERGE_CONFLICT: k-1") || !strings.Contains(ev, "its rebase could not be aborted, so "+wt+" is left mid-rebase") {
		t.Errorf("events:\n%s", ev)
	}
	if !rebaseInProgress(t, wt) {
		t.Error("the fake abort should leave the rebase in progress")
	}
	if got := f.orch.setAside(); len(got) != 1 || got[0] != "k-1" {
		t.Errorf("set aside = %v", got)
	}
}

// After a failed hand-back, an abort that fails is what the ticket's note says, not "aborted".
func TestUndoResolutionWhoseAbortFailsSaysSo(t *testing.T) {
	f := newMergeFixture(t, "true")
	f.orch.merger = noAbort{}
	wt := f.ticket(t, "k-1", "shared.txt", "line 1 from the ticket\n")
	f.onMain(t, "shared.txt", "line 1 from main\n")
	if _, err := (git.Git{}).Rebase(context.Background(), wt, "main"); err == nil {
		t.Fatal("the rebase should stop on the conflict")
	}
	got := f.orch.undoResolution(context.Background(), rebaseStop{worker: worker{id: "k-1", br: "wt/k-1", wt: wt}})
	if want := "the rebase could not be aborted, so " + wt + " is left mid-rebase"; got != want {
		t.Errorf("undoResolution = %q, want %q", got, want)
	}
}

// A returning ticket whose refresh can't be aborted is reported as left mid-rebase.
func TestRefreshBranchWhoseAbortFailsSaysSo(t *testing.T) {
	f := newMergeFixture(t, "true")
	f.orch.merger = noAbort{}
	wt := f.ticket(t, "k-1", "shared.txt", "line 1 from the ticket\n")
	f.onMain(t, "shared.txt", "line 1 from main\n")
	if f.orch.refreshBranch(context.Background(), wt, "wt/k-1") {
		t.Fatal("a branch conflicting with main should not count as refreshed")
	}
	if ev := f.sink.text(); !strings.Contains(ev, "REBASE_ABORT_FAILED: "+wt+" is left mid-rebase onto main") {
		t.Errorf("events:\n%s", ev)
	}
}
