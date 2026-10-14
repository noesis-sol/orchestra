package dispatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/git"
)

// A worktree its check left with uncommitted changes to tracked files, as a formatter or go mod tidy
// does, is set aside as dirty, naming the files, not as a conflict left mid-rebase: git refuses to
// start the rebase, so there is nothing to resolve or abort.
func TestMergeOfADirtyWorktreeSaysItIsDirty(t *testing.T) {
	f := newMergeFixture(t, "true")
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	f.onMain(t, "b.txt", "b\n")
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("a, formatted\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := f.git(f.repo, "rev-parse", "main")
	if s := f.orch.merge(context.Background(), worker{id: "k-1", br: "wt/k-1", wt: wt, tab: "tab"}); s != nil {
		t.Fatal(s)
	}
	ev := f.sink.text()
	if !strings.Contains(ev, "DIRTY_WORKTREE: k-1 closed, but "+wt+" has uncommitted changes to a.txt") {
		t.Errorf("events:\n%s", ev)
	}
	for _, wrong := range []string{"MERGE_CONFLICT", "mid-rebase", "could not say what the rebase stopped on"} {
		if strings.Contains(ev, wrong) {
			t.Errorf("events say %q:\n%s", wrong, ev)
		}
	}
	if f.git(f.repo, "rev-parse", "main") != before {
		t.Error("main must not change")
	}
	if rebaseInProgress(t, wt) {
		t.Error("no rebase should be in progress")
	}
	if got := read(t, filepath.Join(wt, "a.txt")); got != "a, formatted\n" {
		t.Errorf("the uncommitted change should be left as it was, got %q", got)
	}
	if got := f.orch.asideList(); len(got) != 1 || got[0] != "k-1" {
		t.Errorf("set aside = %v", got)
	}
}

// An untracked file doesn't stop a rebase, so it doesn't stop the merge either.
func TestMergeRebasesAWorktreeWithAnUntrackedFile(t *testing.T) {
	f := newMergeFixture(t, "true")
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	f.onMain(t, "b.txt", "b\n")
	if err := os.WriteFile(filepath.Join(wt, "coverage.out"), []byte("mode: set\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s := f.orch.merge(context.Background(), worker{id: "k-1", br: "wt/k-1", wt: wt, tab: "tab"}); s != nil {
		t.Fatal(s)
	}
	if log := f.git(f.repo, "log", "--oneline", "main"); !strings.Contains(log, "k-1: change a.txt") {
		t.Errorf("the ticket was not merged:\n%s\nevents:\n%s", log, f.sink.text())
	}
}

// noStart is git whose rebase fails without starting, as on a worktree git can't check out.
type noStart struct{ git.Git }

func (noStart) Rebase(ctx context.Context, worktree, onto string) (string, error) {
	return "fatal: cannot rebase", errors.New("git rebase main: exit status 128")
}

// A rebase that fails without stopping on conflicts is set aside as such: there is no conflict to
// hand back to the worker, and no rebase to abort.
func TestMergeWhoseRebaseDoesNotStartSaysSo(t *testing.T) {
	f := newMergeFixture(t, "true")
	f.orch.merger = noStart{}
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	f.onMain(t, "b.txt", "b\n")
	if s := f.orch.merge(context.Background(), worker{id: "k-1", br: "wt/k-1", wt: wt, tab: "tab"}); s != nil {
		t.Fatal(s)
	}
	ev := f.sink.text()
	if !strings.Contains(ev, "REBASE_FAILED: k-1 closed, but git could not rebase wt/k-1 onto main, which moved on "+
		"while it ran: git rebase main: exit status 128") {
		t.Errorf("events:\n%s", ev)
	}
	for _, wrong := range []string{"MERGE_CONFLICT", "mid-rebase"} {
		if strings.Contains(ev, wrong) {
			t.Errorf("events say %q:\n%s", wrong, ev)
		}
	}
	if got := f.orch.asideList(); len(got) != 1 || got[0] != "k-1" {
		t.Errorf("set aside = %v", got)
	}
}
