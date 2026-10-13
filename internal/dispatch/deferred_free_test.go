package dispatch

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
)

// A deferred ticket whose blockers have all merged is listed, with the command that reopens it, in
// the START block and in the box of a run with nothing to run; never reopened.

// deferredTickets sets b up with deferred tickets: D, blocked by A, closed and merged; W, blocked by
// I, in progress; L, blocked by U, closed but left unmerged; R, related to A only; N, linked to
// nothing.
func deferredTickets(b *fakeBeads) {
	b.add("A", "merged", 1)
	b.set("A", "closed")
	b.add("I", "in progress", 1)
	b.set("I", "in_progress")
	b.add("U", "conflicted", 1, UnmergedLabel)
	b.set("U", "closed")
	for _, id := range []string{"D", "W", "L", "R", "N"} {
		b.add(id, "deferred", 2)
		b.set(id, "deferred")
	}
	b.link("D", "A", "blocks")
	b.link("W", "I", "blocks")
	b.link("L", "U", "blocks")
	b.link("R", "A", "related")
}

func TestNothingToRunListsTheDeferredTicketsWhoseBlockersMerged(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	b := newFakeBeads()
	deferredTickets(b)
	g := newFakeGit(c.Base)
	wt := filepath.Join(t.TempDir(), "D")
	if _, err := g.NewWorktree(context.Background(), c.Repo, wt, "wt/D", c.Base); err != nil {
		t.Fatal(err)
	}
	n := checkNothingOn(t, c, b, g)
	want := []DeferredFree{{ID: "D", Blockers: []string{"A"}, Worktree: wt}}
	if n == nil || n.FreeErr != nil || !reflect.DeepEqual(n.DeferredFree, want) {
		t.Fatalf("got %+v, want deferred free %+v", n, want)
	}
	if got, cmd := n.DeferredFree[0].Line(c.Base), "git worktree remove "+wt+" && git branch -d wt/D"; got !=
		"DEFERRED_FREE: D: its blockers have merged (A); to run it: bd update D --status open; "+
			"its worktree has no commits beyond main, safe to remove: "+cmd {
		t.Errorf("line %q", got)
	}

	// A worktree with a commit of its own holds work: it isn't offered for removal.
	if err := g.commitIn(wt, "D: started"); err != nil {
		t.Fatal(err)
	}
	n = checkNothingOn(t, c, b, g)
	want[0].Worktree = ""
	if !reflect.DeepEqual(n.DeferredFree, want) {
		t.Errorf("with a commit: got %+v, want %+v", n.DeferredFree, want)
	}
	if got := want[0].Line(c.Base); strings.Contains(got, "worktree") {
		t.Errorf("line %q names a worktree", got)
	}
}

// In a scoped run only the scope's deferred tickets are listed.
func TestNothingToRunScopedListsOnlyItsDeferredTickets(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	b := newFakeBeads()
	deferredTickets(b)
	b.add("P", "parent", 1)
	b.sub("P.1", "P", "child", 1)
	b.set("P.1", "deferred")
	b.link("P.1", "A", "blocks")
	c.Ticket = "P"
	n := checkNothing(t, c, b)
	want := []DeferredFree{{ID: "P.1", Blockers: []string{"A"}}}
	if n == nil || n.FreeErr != nil || !reflect.DeepEqual(n.DeferredFree, want) {
		t.Errorf("got %+v, want deferred free %+v", n, want)
	}
}

// When bd can't show a deferred ticket's blockers, the list is dropped and the check still says
// why there is nothing to run.
func TestNothingToRunDropsTheDeferredListWhenBdFails(t *testing.T) {
	t.Parallel()
	failed := errors.New("bd show: database locked")
	g := newFakeGit("main")
	b := newFakeBeads()
	deferredTickets(b)
	n, err := CheckNothingToRun(context.Background(), nothingConfig(t, 0), unshownBeads{b, failed}, g, g)
	if err != nil || n == nil {
		t.Fatalf("got %+v, %v; want nothing to run", n, err)
	}
	if n.DeferredFree != nil || !errors.Is(n.FreeErr, failed) || n.Deferred != 5 {
		t.Errorf("got %+v; want no list, bd's error and 5 deferred", n)
	}
}

// The START block lists them too, after the tickets left unmerged, and the run leaves them deferred.
func TestStartListsTheDeferredTicketsWhoseBlockersMerged(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		deferredTickets(h.beads)
		wt := h.worktree("D")
		if _, err := h.mem.NewWorktree(t.Context(), h.repo, wt, "wt/D", "main"); err != nil {
			t.Fatal(err)
		}
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		want := "  DEFERRED_FREE: D: its blockers have merged (A); to run it: bd update D --status open; " +
			"its worktree has no commits beyond main, safe to remove: git worktree remove " + wt +
			" && git branch -d wt/D"
		if !strings.Contains(ev, want+"\n") {
			t.Errorf("the run didn't say %q:\n%s", want, ev)
		}
		if n := strings.Count(ev, "DEFERRED_FREE"); n != 1 {
			t.Errorf("%d DEFERRED_FREE lines, want 1:\n%s", n, ev)
		}
		if st, _ := h.beads.Status(t.Context(), "D"); st != StatusDeferred {
			t.Errorf("D is %s, want it left deferred", st)
		}
	})
}

// A bd failure drops the START block's list, with a line in the log, and the run goes on.
func TestStartGoesOnWhenBdCantListTheDeferredTickets(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		deferredTickets(h.beads)
		h.beads.add("T", "ready", 1)
		h.worker("T", finishes("t.txt"))
		o := h.loop()
		failed := errors.New("bd show: database locked")
		o.tickets = deferredUnshown{h.beads, failed}
		if code := o.Run(t.Context()); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if ev := h.sink.text(); strings.Contains(ev, "DEFERRED_FREE") {
			t.Errorf("a list without bd show:\n%s", ev)
		}
		if l := h.logged(); !strings.Contains(l, "cannot list the deferred tickets whose blockers have merged: "+
			failed.Error()) {
			t.Errorf("the log doesn't say why:\n%s", l)
		}
	})
}

// deferredUnshown is a tracker whose bd show fails for deferred tickets.
type deferredUnshown struct {
	*fakeBeads
	err error
}

func (u deferredUnshown) Show(ctx context.Context, id string) (Ticket, error) {
	if st, _ := u.Status(ctx, id); st == StatusDeferred {
		return Ticket{ID: id, Status: StatusUnknown}, u.err
	}
	return u.fakeBeads.Show(ctx, id)
}
