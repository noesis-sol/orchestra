package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// doneOnMain claims the ticket, has file committed on main meanwhile with onMain in it, as by a ticket
// merged before it that did what it was about, commits file itself (with "A\n" in it, for ticket A)
// and closes it, having reverted its commit if revert is set.
func doneOnMain(h *harness, file, onMain string, revert bool) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		if err := os.WriteFile(filepath.Join(h.repo, file), []byte(onMain), 0o644); err != nil {
			w.t.Error(err)
		}
		h.git(h.repo, "add", file)
		h.git(h.repo, "commit", "-q", "-m", "other: add "+file)
		w.commit(file)
		if revert {
			h.git(w.wt, "revert", "--no-edit", "HEAD")
		}
		w.close()
		return "idle"
	}
}

// netEmptyRun runs ticket A, closed with no net change by doneOnMain(onMain, revert), and B, which A
// blocks, with a check that fails: A should close with no change to merge, why being want, without
// a check or a hand-back, leaving main's file as it was and holding nothing up.
func netEmptyRun(t *testing.T, onMain string, revert bool, want string) {
	t.Helper()
	h := newHarness(t)
	h.cfg.Check = "exit 1" // any check run on A's code would set it aside
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.beads.link("B", "A", "blocks")
	h.worker("A", doneOnMain(h, "a.txt", onMain, revert))
	h.worker("B", finishes("b.txt"))
	o, code := h.run()
	ev := h.sink.text()
	if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), ev)
	}
	if !strings.Contains(ev, "A closed with no change: "+want+", so there is nothing to merge; "+
		"worktree, branch and tab removed") {
		t.Errorf("want A closed with no change (%s):\n%s", want, ev)
	}
	for _, not := range []string{"MERGE_CONFLICT", "CHECKS_FAILED", "handed back", "rebased wt/A"} {
		if strings.Contains(ev, not) {
			t.Errorf("A should not be checked, handed back or set aside (%q):\n%s", not, ev)
		}
	}
	if i := slices.IndexFunc(h.sink.events, func(e Event) bool { return e.Ticket == "A" && e.Kind == EvClosed }); i < 0 ||
		h.sink.events[i].Detail != "no change to merge" {
		t.Errorf("want a closed event for A with nothing to merge:\n%s", ev)
	}
	if exists(h.worktree("A")) || h.git(h.repo, "branch", "--list", "wt/A") != "" {
		t.Error("A's worktree and branch should be removed")
	}
	if !slices.Contains(h.herdr.tabsClosed(), "tab1") {
		t.Errorf("A's tab (tab1) should be closed: %v", h.herdr.tabsClosed())
	}
	if a, _ := h.beads.Show(context.Background(), "A"); HasLabel(a, UnmergedLabel) {
		t.Errorf("A should not be labelled %q: %v", UnmergedLabel, a.Labels)
	}
	if log := h.mainLog(); strings.Contains(log, "A: add a.txt") || strings.Contains(log, "Revert") {
		t.Errorf("none of A's commits should reach main:\n%s", log)
	}
	if got := h.git(h.repo, "show", "main:a.txt"); got != onMain {
		t.Errorf("main's a.txt should be left as it is: %q, want %q", got, onMain)
	}
	if log := h.mainLog(); !strings.Contains(log, "B: add b.txt") {
		t.Errorf("B should merge once A closed:\n%s", log)
	}
}

// A worker that finds its change on main already and reverts its own commit leaves a branch whose
// commits change nothing: rebasing it would replay the revert onto main's change. It closes with no
// change to merge before any rebase.
func TestCommitAndItsRevertCloseUnchanged(t *testing.T) {
	t.Parallel()
	netEmptyRun(t, "fixed\n", true, "the commits on wt/A, taken together, change nothing")
}

// A commit whose change main has already is dropped by the rebase, which leaves the branch with
// nothing to merge: it closes so, without running the check on code it doesn't change.
func TestCommitMainHasAlreadyClosesUnchanged(t *testing.T) {
	t.Parallel()
	netEmptyRun(t, "A\n", false, "rebased onto main, which has its changes already, wt/A changes nothing")
}
