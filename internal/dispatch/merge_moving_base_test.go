package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
)

// Only merges move Base during a run, and they queue, so a ticket's merge rebases at most once for
// them; a commit made on Base by hand while the rebased code is checked has it rebased and checked
// again. The merge rebases three times at most, then merges if Base stayed put while the last rebase
// was checked: when Base moves on after each rebase, the ticket is set aside with MERGE_CONFLICT,
// "main kept changing", its branch left as its last rebase left it.

// movingBase is git in memory on which a commit lands on Base, as if made by hand, right after each
// of the first moves rebases.
type movingBase struct {
	*fakeGit
	moves   int
	rebases int    // how many times Rebase ran
	last    string // the rebased branch's log, after the last rebase
}

func (g *movingBase) Rebase(ctx context.Context, worktree, onto string) (string, error) {
	out, err := g.fakeGit.Rebase(ctx, worktree, onto)
	g.rebases++
	g.mu.Lock()
	br := g.worktrees[worktree]
	g.mu.Unlock()
	g.last = g.log(br)
	if g.rebases <= g.moves {
		f := "by-hand-" + string(rune('0'+g.rebases)) + ".txt"
		g.commit(g.base, "by hand: "+f, f)
	}
	return out, err
}

func TestAMergeWhileBaseKeepsMoving(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		moves   int  // the rebases after which Base moves on
		merged  bool // whether A merges, or is set aside
		rebases int
	}{
		{"never after its rebase", 0, true, 1},
		{"after its first rebase", 1, true, 2},
		// The third rebase, onto a Base that then stays put, is checked and merged.
		{"after its first two rebases", 2, true, 3},
		{"after every rebase", 3, false, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "fixed"), nil, 0o644); err != nil {
					t.Fatal(err)
				}
				h.cfg.Check = countedCheck(dir, "") // passes
				h.beads.add("A", "first", 1)
				h.worker("A", func(w *fakeWorker) AgentState {
					w.claim()
					w.commit("a.txt")
					w.git.commit("main", "landed on main: landed.txt", "landed.txt")
					w.close()
					return "idle"
				})
				g := &movingBase{fakeGit: h.mem, moves: tc.moves}
				o := h.loop()
				o.merger = g
				o.Run(context.Background())
				ev, main := h.sink.text(), h.mem.log("main")
				if g.rebases != tc.rebases {
					t.Errorf("A was rebased %d times, want %d", g.rebases, tc.rebases)
				}
				if n := checkRuns(t, dir, "A"); n != tc.rebases {
					t.Errorf("A's check ran %d times, want %d, once a rebase", n, tc.rebases)
				}
				if tc.merged {
					if !strings.Contains(main, "A: add a.txt") {
						t.Errorf("A should be merged:\n%s", main)
					}
					if strings.Contains(ev, "MERGE_CONFLICT") {
						t.Errorf("events say MERGE_CONFLICT:\n%s", ev)
					}
					return
				}
				want := "  MERGE_CONFLICT: A closed, but main kept changing while its checks ran (commits made by " +
					"hand?); worktree " + h.worktree("A") + " and tab " + tabOf(h, "A") + " left for review"
				var aside []Event
				for _, e := range eventsOf(h, EvWarn) {
					if e.Aside {
						aside = append(aside, e)
					}
				}
				if len(aside) != 1 || aside[0].Ticket != "A" || aside[0].Text != want ||
					aside[0].Detail != "main kept changing" || aside[0].Blocked != "main kept changing" {
					t.Errorf("set aside %+v, want A with %q", aside, want)
				}
				if log := h.logged(); !strings.Contains(log, want) {
					t.Errorf("log lacks %q:\n%s", want, log)
				}
				if got := o.asideList(); !equal(got, []string{"A"}) {
					t.Errorf("set aside %v, want A", got)
				}
				if !unmergedLabel(t, h, "A") {
					t.Error("A should be labelled unmerged")
				}
				if strings.Contains(main, "A: ") {
					t.Errorf("A should not be merged:\n%s", main)
				}
				if got := h.mem.log("wt/A"); got != g.last {
					t.Errorf("wt/A is\n%s\nwant it as its last rebase left it:\n%s", got, g.last)
				}
				if !exists(h.worktree("A")) {
					t.Errorf("A's worktree %s should be left for review", h.worktree("A"))
				}
			})
		})
	}
}
