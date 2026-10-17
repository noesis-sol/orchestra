package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A was closed by hand with its commit only on wt/A, and B, which A blocks, waits for it. C's worker
// deletes wt/A meanwhile, as CLOSED_UNMERGED offers, while main stays where it is: B starts at the
// next poll, not once C merges and main moves on.
func TestDeletingAClosedBlockersBranchFreesItsDependents(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "closed by hand", 1)
		h.beads.set("A", StatusClosed)
		cutBranch(h.mem, "wt/A")
		h.mem.commit("wt/A", "A: add a.txt", "a.txt")
		h.beads.add("C", "beside", 1)
		h.beads.add("B", "blocked by A", 2)
		h.beads.link("B", "A", "blocks")
		h.worker("C", func(w *fakeWorker) AgentState {
			time.Sleep(2 * time.Minute) // B is held meanwhile, poll after poll
			if out, err := w.git.DeleteBranch(context.Background(), "", "wt/A"); err != nil {
				t.Errorf("deleting wt/A: %v: %s", err, out)
			}
			time.Sleep(time.Hour) // B starts, finishes and merges meanwhile
			return finishes("c.txt")(w)
		})
		h.worker("B", finishes("b.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.dispatched(); !equal(got, []string{"C", "B"}) {
			t.Errorf("dispatched %v, want C, then B once wt/A was deleted", got)
		}
		if got := closedIDs(h); !equal(got, []string{"B", "C"}) {
			t.Errorf("merged %v, want B before C: B waited for main to move on", got)
		}
		ev := h.sink.text()
		if want := "  B waits: A closed but not merged (wt/A is not on main)"; strings.Count(ev, want) != 1 {
			t.Errorf("events say %q %d times, want once:\n%s", want, strings.Count(ev, want), ev)
		}
		if n := strings.Count(ev, "CLOSED_UNMERGED"); n != 1 {
			t.Errorf("%d CLOSED_UNMERGED warnings, want one:\n%s", n, ev)
		}
	})
}

// A branch found not on Base is asked of again only once it is on another commit or gone: on the
// same commits, git's answer is kept.
func TestClosedBranchesAsksAgainOnceTheBranchMoves(t *testing.T) {
	t.Parallel()
	var b closedBranches
	tip, why, asked := "t1", "wt/A is not on main", 0
	ask := func() string {
		got, _ := b.why("A", "h1", func() string { return tip }, func() string { asked++; return why })
		return got
	}
	for range 3 {
		if got := ask(); got != "wt/A is not on main" || asked != 1 {
			t.Fatalf("why %q, git asked %d times; want wt/A not on main, asked once", got, asked)
		}
	}
	tip = "t2" // a commit added to wt/A
	if got := ask(); got != "wt/A is not on main" || asked != 2 {
		t.Fatalf("why %q, git asked %d times; want wt/A not on main, asked again", got, asked)
	}
	tip, why = "", "" // wt/A deleted
	for range 3 {
		if got := ask(); got != "" || asked != 3 {
			t.Fatalf("why %q, git asked %d times; want nothing held, asked once more", got, asked)
		}
	}
}
