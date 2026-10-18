package dispatch

import (
	"context"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// orchestra-ucdx: a worker whose terminal drops its name (see unnamed) is looked for in its pane and
// named again on the paths that follow it once its ticket is out of its run, as the settle loop does:
// left in its tab as its ticket was set aside, and handed its finished ticket back.

// A's worker settles with A open, and its name is dropped in the tab it was left in: it is named
// again and still followed, so that A, closed there later, is merged as a late close.
func TestLeftOpenWorkerThatLosesItsNameIsStillFollowed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "left open", 1)
		h.beads.add("C", "beside", 2)
		h.worker("A", leavesOpen)
		h.worker("C", func(w *fakeWorker) AgentState {
			time.Sleep(startGrace + 2*time.Minute) // A is set aside meanwhile
			unnamed(h.herdr, "A", nil)
			time.Sleep(2 * time.Minute)
			closesInTab(h, "A", "a.txt")
			time.Sleep(2 * time.Minute)
			return finishes("c.txt")(w)
		})

		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		tab := tabOf(h, "A")
		ev := h.sink.text()
		for _, want := range []string{
			"  A's worker lost its name in tab " + tab + "; named it A again\n",
			"  LATE_CLOSE: A was closed in tab " + tab + " after it was set aside; merging it now",
		} {
			if strings.Count(ev, want) != 1 {
				t.Errorf("events say %q %d times, want once:\n%s", want, strings.Count(ev, want), ev)
			}
		}
		if strings.Contains(ev, "is gone with it") {
			t.Errorf("A's worker was taken for gone:\n%s", ev)
		}
		if got := closedIDs(h); !slices.Contains(got, "A") {
			t.Errorf("merged %v; want A among them", got)
		}
		if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") {
			t.Errorf("main lacks A's commit:\n%s", log)
		}
	})
}

// unnamingRebase is git, but ticket id's worker loses its name as its worktree wt is rebased.
type unnamingRebase struct {
	Merger
	h      *fakeHerdr
	id, wt string
}

func (m unnamingRebase) Rebase(ctx context.Context, worktree, onto string) (string, error) {
	if worktree == m.wt {
		unnamed(m.h, m.id, nil)
	}
	return m.Merger.Rebase(ctx, worktree, onto)
}

// resolvesInMemory resolves the rebase stopped on conflicts in its worktree, in git in memory: the
// branch is rebased onto main and the rebase is over.
func resolvesInMemory(g *fakeGit) behaviour {
	return func(w *fakeWorker) AgentState {
		g.mu.Lock()
		delete(g.conflicts, branchOf(w.id))
		g.mu.Unlock()
		if _, err := g.Rebase(context.Background(), w.wt, "main"); err != nil {
			w.t.Error(err)
		}
		g.mu.Lock()
		delete(g.rebasing, w.wt)
		g.mu.Unlock()
		return "idle"
	}
}

// A's worker loses its name after it closed A, as A's rebase stops on conflicts: it is named again
// and handed the rebase back, resolves it, and A merges.
func TestWorkerThatLosesItsNameBeforeAHandBackGetsIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Check = "true"
		h.cfg.ResolveConflicts = true
		h.mem.conflict("wt/A", "shared.txt")
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.git.commit("main", "landed on main: shared.txt", "shared.txt")
			w.commit("shared.txt")
			w.close()
			return "idle"
		}, resolvesInMemory(h.mem))
		o := h.loop()
		o.merger = unnamingRebase{Merger: o.merger, h: h.herdr, id: "A", wt: h.worktree("A")}
		if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"  A's worker lost its name in tab tab1; named it A again\n",
			"RESOLVING: wt/A conflicts with main in shared.txt; handed back to its worker in tab tab1",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("no %q in events:\n%s", want, ev)
			}
		}
		if strings.Contains(ev, "MERGE_CONFLICT") || strings.Contains(ev, "its worker is gone") {
			t.Errorf("A was set aside:\n%s", ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v; want A", got)
		}
		if !equal(h.herdr.pastedTo(), []string{"A"}) {
			t.Errorf("prompts pasted to %v, want the hand-back to A", h.herdr.pastedTo())
		}
	})
}
