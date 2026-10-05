package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

// fixedOnMain claims the ticket, has file committed on main meanwhile, as by a ticket merged before
// it that did what it was about, and closes it with nothing of its own.
func fixedOnMain(h *harness, file string) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		if err := os.WriteFile(filepath.Join(h.repo, file), []byte("fixed\n"), 0o644); err != nil {
			w.t.Error(err)
		}
		h.git(h.repo, "add", file)
		h.git(h.repo, "commit", "-q", "-m", "other: add "+file)
		w.close()
		return "idle"
	}
}

// A ticket closed with an empty branch and a clean worktree has nothing to merge: its worktree,
// branch and tab go as after a merge, its unmerged label from an earlier run too, and the tickets it
// blocks run. One whose branch has a commit that doesn't name it is still left for review.
func TestTicketClosedWithNoChangeIsCleanedUp(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1, UnmergedLabel) // reopened after an earlier run left it unmerged
	h.beads.add("B", "second", 2)
	h.beads.add("C", "third", 3)
	h.beads.link("B", "A", "blocks")
	h.worker("A", fixedOnMain(h, "a.txt"))
	h.worker("B", finishes("b.txt"))
	h.worker("C", closesUnnamed("c.txt"))
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	ev := h.sink.text()
	if !strings.Contains(ev, "A closed with no change: wt/A has no commits beyond main and its worktree is clean, "+
		"so there is nothing to merge; worktree, branch and tab removed") {
		t.Errorf("events:\n%s", ev)
	}
	if strings.Contains(ev, "CLOSED_WITHOUT_COMMIT: no commit on wt/A") || strings.Contains(ev, "B waits") {
		t.Errorf("A should not be left for review, nor hold B:\n%s", ev)
	}
	if i := slices.IndexFunc(h.sink.events, func(e Event) bool { return e.Ticket == "A" && e.Kind == EvClosed }); i < 0 ||
		h.sink.events[i].Detail != "no change to merge" || h.sink.events[i].Title != "first" {
		t.Errorf("want a closed event for A with nothing to merge:\n%s", ev)
	}
	if exists(h.worktree("A")) {
		t.Error("A's worktree should be removed")
	}
	if br := h.git(h.repo, "branch", "--list", "wt/A"); br != "" {
		t.Errorf("A's branch should be deleted: %q", br)
	}
	if closed := h.herdr.tabsClosed(); !slices.Contains(closed, "tab1") || slices.Contains(closed, "tab3") {
		t.Errorf("tabs closed %v, want A's (tab1) and not C's (tab3)", closed)
	}
	if a, _ := h.beads.Show(context.Background(), "A"); HasLabel(a, UnmergedLabel) {
		t.Errorf("A has nothing to merge, so it should not be labelled %q: %v", UnmergedLabel, a.Labels)
	}
	if log := h.mainLog(); !strings.Contains(log, "B: add b.txt") {
		t.Errorf("B should merge once A closed:\n%s", log)
	}

	if !strings.Contains(ev, "CLOSED_WITHOUT_COMMIT: no commit on wt/C names C") {
		t.Errorf("C has a commit that doesn't name it, so it should be left for review:\n%s", ev)
	}
	if !exists(h.worktree("C")) || h.git(h.repo, "branch", "--list", "wt/C") == "" {
		t.Error("C's worktree and branch should be left for review")
	}
	if c, _ := h.beads.Show(context.Background(), "C"); !HasLabel(c, UnmergedLabel) {
		t.Errorf("C should be labelled %q: %v", UnmergedLabel, c.Labels)
	}
}

// The same with git in memory, for go test -short: a ticket its worker closes having committed
// nothing leaves no worktree, branch, tab or label behind, and counts as closed in the run.
func TestTicketClosedWithNothingCommittedIsCleanedUp(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.link("B", "A", "blocks")
		h.worker("A", closesWithoutCommit)
		h.worker("B", finishes("b.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if ev := h.sink.text(); !strings.Contains(ev, "A closed with no change") || strings.Contains(ev, "CLOSED_WITHOUT_COMMIT") {
			t.Errorf("events:\n%s", ev)
		}
		if exists(h.worktree("A")) || slices.Contains(h.mem.branchList(), "wt/A") {
			t.Errorf("A's worktree and branch should be removed: branches %v", h.mem.branchList())
		}
		if !slices.Contains(h.herdr.tabsClosed(), "tab1") {
			t.Errorf("A's tab (tab1) should be closed: %v", h.herdr.tabsClosed())
		}
		if a, _ := h.beads.Show(context.Background(), "A"); HasLabel(a, UnmergedLabel) {
			t.Errorf("A should not be labelled %q: %v", UnmergedLabel, a.Labels)
		}
		want := []string{"Closed A · first", "Closed B · second", "Finished the run · 2 tickets closed"}
		if got := h.alerts.list(); !equal(got, want) {
			t.Errorf("notified:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
		}
	})
}
