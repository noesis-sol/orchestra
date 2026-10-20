package dispatch

import (
	"os"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

// A carried-over worker whose ticket was closed and merged by hand between runs is dropped with its
// worktree, branch and tab removed, as after a merge: its branch fast-forwarded onto main, or its
// change squashed there (the branch then moved to main first, so that git deletes it). One whose
// worktree has uncommitted changes keeps them, with its branch and tab, and the run says so.
func TestCarriedOverWorkerMergedByHandIsCleanedUp(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		between func(t *testing.T, h *harness)
		removed bool   // A's worktree, branch and tab removed
		reset   bool   // wt/A moved to main before its removal
		says    string // after "is on main already (…)"
	}{
		{"fast-forwarded", func(t *testing.T, h *harness) {
			if _, err := h.mem.FastForward(t.Context(), h.repo, "wt/A"); err != nil {
				t.Fatal(err)
			}
		}, true, false, "; worktree, branch and tab removed"},
		{"squashed", func(_ *testing.T, h *harness) {
			h.mem.commit("main", "A: add a.txt, squashed", "a.txt")
		}, true, true, "; worktree, branch and tab removed"},
		{"uncommitted changes", func(t *testing.T, h *harness) {
			if _, err := h.mem.FastForward(t.Context(), h.repo, "wt/A"); err != nil {
				t.Fatal(err)
			}
			h.mem.leaveChange(h.worktree("A"), "b.txt")
		}, false, false, "; worktree WT has uncommitted changes, so it, wt/A and tab tab1 are left as they are " +
			"(commit or remove them, then remove it)"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.beads.add("A", "first", 1)
				h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
				if o, code := h.run(); code != ExitStuck || len(saved(t, h)) != 1 {
					t.Fatalf("run 1: exit %d, final %q, saved %+v\n%s", code, o.Final(), saved(t, h), h.sink.text())
				}
				// Its worker commits in its tab after the run, and the maintainer merges it by hand.
				if err := h.mem.commitIn(h.worktree("A"), "A: add a.txt", "a.txt"); err != nil {
					t.Fatal(err)
				}
				c.between(t, h)
				h.beads.set("A", "closed")

				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
					t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				ev := h.sink.text()
				says := strings.ReplaceAll(c.says, "WT", h.worktree("A"))
				want := "  A, carried over from the last run, is dropped: wt/A is on main already (" +
					h.mem.naming("main", "A") + ")" + says + "\n"
				if !strings.Contains(ev, want) {
					t.Errorf("run 2 events lack %q:\n%s", want, ev)
				}
				_, statErr := os.Stat(h.worktree("A"))
				if gone := os.IsNotExist(statErr); gone != c.removed {
					t.Errorf("A's worktree removed: %v (%v), want %v", gone, statErr, c.removed)
				}
				h.mem.mu.Lock()
				_, kept := h.mem.branches["wt/A"]
				reset := len(h.mem.replaced) > 0
				h.mem.mu.Unlock()
				if kept == c.removed {
					t.Errorf("wt/A kept: %v, want %v", kept, !c.removed)
				}
				if reset != c.reset {
					t.Errorf("wt/A moved to main: %v, want %v", reset, c.reset)
				}
				if got := slices.Contains(h.herdr.tabsClosed(), "tab1"); got != c.removed {
					t.Errorf("tab1 closed: %v, want %v", got, c.removed)
				}
				if got := saved(t, h); len(got) != 0 {
					t.Errorf("run 2 left %+v; want nothing", got)
				}
			})
		})
	}
}
