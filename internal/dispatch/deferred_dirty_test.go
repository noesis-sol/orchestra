package dispatch

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
)

// A deferred ticket whose worktree has no commits of its own but uncommitted changes holds work:
// its worktree isn't called safe to remove, in the box of a run with nothing to run or the START
// block, and the line says what keeps it.

// dirtyWorktrees is git in memory where the worktrees in dirty have uncommitted changes.
type dirtyWorktrees struct {
	*fakeGit
	dirty map[string]bool
}

func (g dirtyWorktrees) DirtyWorktree(ctx context.Context, dir string) string {
	if g.dirty[dir] {
		return "?? wip.txt"
	}
	return g.fakeGit.DirtyWorktree(ctx, dir)
}

func TestNothingToRunKeepsADeferredTicketsDirtyWorktree(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	b := newFakeBeads()
	deferredTickets(b)
	g := newFakeGit(c.Base)
	wt := filepath.Join(t.TempDir(), "D")
	if _, err := g.NewWorktree(context.Background(), c.Repo, wt, "wt/D", c.Base); err != nil {
		t.Fatal(err)
	}
	n, err := CheckNothingToRun(context.Background(), c, b, dirtyWorktrees{g, map[string]bool{wt: true}}, g)
	if err != nil || n == nil {
		t.Fatalf("got %+v, %v; want nothing to run", n, err)
	}
	want := []DeferredFree{{ID: "D", Blockers: []string{"A"}, Uncommitted: wt}}
	if n.FreeErr != nil || !reflect.DeepEqual(n.DeferredFree, want) {
		t.Fatalf("got %+v, want deferred free %+v", n, want)
	}
	got := n.DeferredFree[0].Line(c.Base)
	if want := "DEFERRED_FREE: D: its blockers have merged (A); to run it: bd update D --status open; " +
		"its worktree holds uncommitted changes, not safe to remove: git -C " + wt + " status"; got != want {
		t.Errorf("line %q, want %q", got, want)
	}
	if n.DeferredFree[0].Cleanup() != "" {
		t.Errorf("a cleanup command for a dirty worktree: %q", n.DeferredFree[0].Cleanup())
	}
}

func TestStartKeepsADeferredTicketsDirtyWorktree(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		deferredTickets(h.beads)
		wt := h.worktree("D")
		if _, err := h.mem.NewWorktree(t.Context(), h.repo, wt, "wt/D", "main"); err != nil {
			t.Fatal(err)
		}
		o := h.loop()
		o.checkout = dirtyWorktrees{h.mem, map[string]bool{wt: true}}
		if code := o.Run(t.Context()); code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		want := "  DEFERRED_FREE: D: its blockers have merged (A); to run it: bd update D --status open; " +
			"its worktree holds uncommitted changes, not safe to remove: git -C " + wt + " status"
		if !strings.Contains(ev, want+"\n") {
			t.Errorf("the run didn't say %q:\n%s", want, ev)
		}
		if strings.Contains(ev, "safe to remove: git worktree remove") {
			t.Errorf("the run offers to remove a dirty worktree:\n%s", ev)
		}
	})
}
