package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
)

// A merge rebases the ticket's branch onto the commit it read Base at, not onto Base by name: a commit
// made on Base by hand between the read and the rebase would otherwise land the branch on the newer
// commit while the rebase's onto, which the setup's diff and the resolution check count from, names
// the older one. The merge then sees Base moved on, rebases again onto the newer commit and merges.

// baseMovedBeforeRebase is git in memory on which a commit lands on Base, as if made by hand, as the
// merge counts the branch's own commits, after it read where Base is and before it rebases.
type baseMovedBeforeRebase struct {
	*fakeGit
	br    string
	read  string   // where Base was as the merge read it, before the commit by hand
	ontos []string // what each rebase was onto
}

func (g *baseMovedBeforeRebase) CountCommits(ctx context.Context, repo, revs string) (int, error) {
	n, err := g.fakeGit.CountCommits(ctx, repo, revs)
	if onto, ok := strings.CutSuffix(revs, ".."+g.br); ok && g.read == "" && onto != "main" {
		g.read = onto
		g.commit("main", "by hand: by-hand.txt", "by-hand.txt")
	}
	return n, err
}

func (g *baseMovedBeforeRebase) Rebase(ctx context.Context, worktree, onto string) (string, error) {
	g.ontos = append(g.ontos, onto)
	return g.fakeGit.Rebase(ctx, worktree, onto)
}

func TestARebaseOntoTheCommitItRead(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.commit("a.txt")
			w.git.commit("main", "landed on main: landed.txt", "landed.txt")
			w.close()
			return "idle"
		})
		g := &baseMovedBeforeRebase{fakeGit: h.mem, br: "wt/A"}
		o := h.loop()
		o.merger = g
		o.Run(context.Background())
		if g.read == "" {
			t.Fatal("main never moved between the read of where it is and the rebase")
		}
		if len(g.ontos) < 2 || g.ontos[0] != g.read {
			t.Errorf("rebased onto %v, want first onto %s, the commit read, then onto main as it moved on",
				g.ontos, g.read)
		}
		main := h.mem.log("main")
		for _, want := range []string{"A: add a.txt", "by hand: by-hand.txt", "landed on main: landed.txt"} {
			if !strings.Contains(main, want) {
				t.Errorf("main lacks %q:\n%s", want, main)
			}
		}
		if h.sink.warned("MERGE_CONFLICT", "") {
			t.Errorf("events say MERGE_CONFLICT:\n%s", h.sink.text())
		}
	})
}
