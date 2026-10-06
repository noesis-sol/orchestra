package dispatch

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/git"
)

// A ticket left unmerged counts as merged by hand only through commits its own branch added since
// it was cut from main (ForkKey): an older commit on main naming it, such as another ticket's
// mentioning it, is not its merge.

// leavesWorkUncommitted claims the ticket, writes file without committing it and closes the ticket:
// its branch has no commits of its own.
func leavesWorkUncommitted(file string) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		if err := os.WriteFile(filepath.Join(w.wt, file), []byte("unfinished\n"), 0o644); err != nil {
			w.t.Error(err)
		}
		w.close()
		return "idle"
	}
}

// mentionOnMain commits file on main with a message whose body names ticket, as another ticket's
// commit may.
func mentionOnMain(h *harness, file, ticket string) {
	if err := os.WriteFile(filepath.Join(h.repo, file), []byte("other\n"), 0o644); err != nil {
		h.t.Fatal(err)
	}
	h.git(h.repo, "add", file)
	h.git(h.repo, "commit", "-q", "-m", "other: add "+file, "-m", "Leaves the rest to "+ticket+".")
}

// checkNothing runs the check for nothing to run on h's repository and tracker: nil when a run would
// start a ticket.
func (h *harness) checkNothing() *NothingToRun {
	h.t.Helper()
	n, err := CheckNothingToRun(context.Background(), h.cfg, h.beads, git.Git{}, git.Git{})
	if err != nil {
		h.t.Fatal(err)
	}
	return n
}

// A ticket closed with nothing committed, its branch on main with no commits of its own, stays
// unmerged in the next run though a commit on main from before its branch was cut names it: its
// label stays, the ticket it blocks waits, and the check for nothing to run agrees.
func TestUnmergedBranchWithNoCommitsIsNotMergedByAnOlderCommit(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	mentionOnMain(h, "other.txt", "A")
	fork := strings.TrimSpace(h.git(h.repo, "rev-parse", "main"))
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.beads.link("B", "A", "blocks")
	h.worker("A", leavesWorkUncommitted("a.txt"))
	h.worker("B", finishes("b.txt"))
	if o, code := h.run(); code != ExitOK {
		t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if ev := h.sink.text(); !strings.Contains(ev, "CLOSED_WITHOUT_COMMIT: no commit on wt/A names A") {
		t.Fatalf("run 1 should leave A unmerged:\n%s", ev)
	}
	if got := h.beads.metadata("A", ForkKey); got != fork {
		t.Errorf("A's %s = %q, want main as wt/A was cut from it, %s", ForkKey, got, fork)
	}
	if n := h.checkNothing(); n == nil || n.Unmerged != 1 {
		t.Errorf("before run 2, the check found %+v, want A unmerged and nothing to run", n)
	}

	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
		t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	ev := h.sink.text()
	if !strings.Contains(ev, "A was left unmerged by an earlier run") || strings.Contains(ev, "is on main now") {
		t.Errorf("run 2 should hold A as unmerged:\n%s", ev)
	}
	if a, _ := h.beads.Show(context.Background(), "A"); !HasLabel(a, UnmergedLabel) {
		t.Errorf("A should keep its %q label: %v", UnmergedLabel, a.Labels)
	}
	if log := h.mainLog(); strings.Contains(log, "B: add b.txt") {
		t.Errorf("B should wait for A:\n%s", log)
	}
	if n := h.checkNothing(); n == nil || n.Unmerged != 1 {
		t.Errorf("after run 2, the check found %+v, want A unmerged and nothing to run", n)
	}
}

// A ticket left unmerged whose branch is merged by hand, main having moved on since and a commit
// before its branch named it, is merged in the next run: its label goes and the ticket it blocks
// runs.
func TestUnmergedBranchMergedByHandIsMerged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	mentionOnMain(h, "other.txt", "A")
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.beads.link("B", "A", "blocks")
	h.worker("A", leavesUncommitted("a.txt", ".gitignore"))
	h.worker("B", finishes("b.txt"))
	if o, code := h.run(); code != ExitOK {
		t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if a, _ := h.beads.Show(context.Background(), "A"); !HasLabel(a, UnmergedLabel) {
		t.Fatalf("run 1 should leave A unmerged:\n%s", h.sink.text())
	}
	h.git(h.repo, "merge", "-q", "--ff-only", "wt/A") // by hand, leaving the uncommitted change out
	mentionOnMain(h, "later.txt", "C")
	if n := h.checkNothing(); n != nil {
		t.Errorf("before run 2, the check found %+v, want B to run", n)
	}

	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if ev := h.sink.text(); !strings.Contains(ev, "A, left unmerged by an earlier run, is on main now") {
		t.Errorf("run 2 should find A merged:\n%s", ev)
	}
	if a, _ := h.beads.Show(context.Background(), "A"); HasLabel(a, UnmergedLabel) {
		t.Errorf("A's %q label should be removed: %v", UnmergedLabel, a.Labels)
	}
	if log := h.mainLog(); !strings.Contains(log, "B: add b.txt") {
		t.Errorf("B should merge once A is on main:\n%s", log)
	}
}

// A worker left running whose ticket is closed between runs is dropped by the next run as merged
// only when its branch's own commits are on main: one with no commits of its own, cut from main
// just after an earlier attempt at the ticket merged, is taken up instead and finished.
func TestCarriedOverWorkerIsDroppedOnlyForItsOwnCommits(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		between func(t *testing.T, h *harness)
		dropped bool
	}{
		{"merged by hand", func(t *testing.T, h *harness) {
			if err := h.mem.commitIn(h.worktree("A"), "A: add a.txt", "a.txt"); err != nil {
				t.Fatal(err)
			}
			if _, err := h.mem.FastForward(t.Context(), h.repo, "wt/A"); err != nil {
				t.Fatal(err)
			}
			h.mem.commit("main", "C: add c.txt", "c.txt")
		}, true},
		{"nothing of its own", func(t *testing.T, h *harness) {}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.mem.commit("main", "A: an earlier attempt", "a0.txt") // A was reopened since
				h.beads.add("A", "first", 1)
				h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
				if o, code := h.run(); code != ExitStuck {
					t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				c.between(t, h)
				h.beads.set("A", "closed")

				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
					t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				ev := h.sink.text()
				if got := strings.Contains(ev, "A, carried over from the last run, is dropped: wt/A is on main already"); got != c.dropped {
					t.Errorf("A dropped as merged: %v, want %v:\n%s", got, c.dropped, ev)
				}
				if !c.dropped && !strings.Contains(ev, "A closed with no change") {
					t.Errorf("A should be taken up and closed with nothing to merge:\n%s", ev)
				}
			})
		})
	}
}

// forkOf takes a commit hash from the ticket's metadata, object or string, and nothing else.
func TestForkOfTakesOnlyACommitHash(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{
		`{"unmerged_fork": "0123abcd"}`:         "0123abcd",
		`"{\"unmerged_fork\": \"0123abcd\"}"`:   "0123abcd",
		`{"unmerged_fork": "--output=/tmp/x"}`:  "",
		`{"unmerged_fork": "0123abc..main"}`:    "",
		`{"unmerged_fork": "0123"}`:             "",
		`{"unmerged_fork": null, "files": "a"}`: "",
		`{"unmerged_fork": 1234567}`:            "",
		`{}`:                                    "",
		``:                                      "",
	} {
		if got := forkOf(Ticket{Metadata: json.RawMessage(raw)}); got != want {
			t.Errorf("forkOf(%s) = %q, want %q", raw, got, want)
		}
	}
}
