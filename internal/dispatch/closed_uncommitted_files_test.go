package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// leavesChangedAndUntracked commits a.txt, then changes it again and leaves a stray build output,
// untracked, without committing either, and closes the ticket.
func leavesChangedAndUntracked(w *fakeWorker) AgentState {
	w.claim()
	w.commit("a.txt")
	w.write(w.wt, "a.txt", "changed again\n")
	if err := os.MkdirAll(filepath.Join(w.wt, "cmd", "orchestra"), 0o755); err != nil {
		w.t.Error(err)
	}
	w.write(w.wt, filepath.Join("cmd", "orchestra", "orchestra"), "binary\n")
	w.close()
	return "idle"
}

func TestClosedUncommittedNamesTheFiles(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.worker("A", leavesChangedAndUntracked)
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	files := "uncommitted: M a.txt, ?? cmd/orchestra/orchestra"
	todo := "commit them or remove them, then merge wt/A into main"
	if ev := h.sink.text(); !strings.Contains(ev, "CLOSED_UNCOMMITTED: A closed") ||
		!strings.Contains(ev, files) || !strings.Contains(ev, todo) {
		t.Errorf("the warning should name %q and say %q:\n%s", files, todo, ev)
	}
	if notes := h.beads.notesOf("A"); !strings.Contains(notes, files) || !strings.Contains(notes, todo) {
		t.Errorf("A's note should name %q and say %q:\n%s", files, todo, notes)
	}
	if log := h.mainLog(); strings.Contains(log, "A: add a.txt") {
		t.Errorf("A should not be merged:\n%s", log)
	}
}

// changesGit is fakeGit with a worktree's changes as git status gives them.
type changesGit struct {
	*fakeGit
	changes map[string]string
}

func (g changesGit) Changes(ctx context.Context, repo string, paths ...string) map[string]string {
	return g.changes
}

func TestUncommittedListNamesAFewThenCounts(t *testing.T) {
	t.Parallel()
	changes := map[string]string{".orchestra/run/prompt.md": "??", "b.go": " M", "a.go": "M "}
	for i := range 6 {
		changes[fmt.Sprintf("out/%d", i)] = "??"
	}
	o := &Loop{}
	o.checkout = changesGit{fakeGit: newFakeGit("main"), changes: changes}
	want := "uncommitted: M a.go, M b.go, ?? out/0, ?? out/1, ?? out/2 and 3 more"
	if got := o.uncommittedList(context.Background(), "wt"); got != want {
		t.Errorf("uncommittedList = %q, want %q", got, want)
	}
	o.checkout = changesGit{fakeGit: newFakeGit("main"), changes: map[string]string{".orchestra/run/x": "??"}}
	if got := o.uncommittedList(context.Background(), "wt"); got != "" {
		t.Errorf("uncommittedList with only .orchestra/run/ = %q, want none", got)
	}
}
