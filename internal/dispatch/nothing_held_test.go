package dispatch

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
)

// A ticket bd ready lists that the loop would hold back from its start is not one the run would
// start: a parent whose subtickets are not all closed and merged, or a ticket a closed but unmerged
// ticket blocks. With every ready ticket held, the run has nothing to run.

// A parent waits for its subtickets: while one is open, or closed but not merged, nothing is
// ready; once they are merged, the parent runs.
func TestNothingToRunHoldsAParentWaitingForItsSubtickets(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	b := newFakeBeads()
	b.add("E", "epic", 1)
	b.kind("E", "epic")
	b.add("P", "parent", 1)
	b.sub("P.1", "P", "child", 1)
	b.set("P.1", "in_progress")
	g := newFakeGit(c.Base)
	n := checkNothingOn(t, c, b, g)
	want := &NothingToRun{HeldParents: 1, InProgress: 1, Done: "READY_EMPTY after 0 tickets: nothing ready"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("child in progress: got  %+v\nwant %+v", n, want)
	}

	b.mu.Lock()
	b.tickets["P.1"].Labels = []string{UnmergedLabel}
	b.mu.Unlock()
	b.set("P.1", "closed")
	n = checkNothingOn(t, c, b, g)
	want = &NothingToRun{HeldParents: 1, Unmerged: 1, Done: "READY_EMPTY after 0 tickets: nothing ready"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("child closed but not merged: got  %+v\nwant %+v", n, want)
	}

	g.commit(c.Base, "P.1: add a.txt", "a.txt") // merged by hand since
	if n := checkNothingOn(t, c, b, g); n != nil {
		t.Errorf("P's child is merged, so P runs, yet nothing to run: %+v", n)
	}
}

// A ticket blocked by one closed but not merged waits for it to merge, though bd ready lists it;
// another ready ticket that nothing holds is something to run.
func TestNothingToRunHoldsATicketBlockedByAnUnmergedOne(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 3)
	b := newFakeBeads()
	b.add("U", "conflicted", 1, UnmergedLabel)
	b.set("U", "closed")
	b.add("B", "blocked by U", 1)
	b.link("B", "U", "blocks")
	b.add("B2", "blocked by U too", 2)
	b.link("B2", "U", "blocks")
	n := checkNothing(t, c, b)
	want := &NothingToRun{HeldBlocked: 2, Unmerged: 1, Done: "READY_EMPTY after 3 tickets: nothing ready"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("got  %+v\nwant %+v", n, want)
	}
	if n.Open() != 2 {
		t.Errorf("open %d, want 2", n.Open())
	}

	b.add("C", "free", 3)
	if n := checkNothing(t, c, b); n != nil {
		t.Errorf("C is ready and nothing holds it, yet nothing to run: %+v", n)
	}
}

// A scoped run whose ready tickets are all held has nothing to run, and says why each ticket isn't
// done as SCOPE_OPEN does.
func TestNothingToRunScopedHoldsAsTheLoopDoes(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	b := newFakeBeads()
	b.add("P", "parent", 1)
	b.sub("P.1", "P", "child", 1)
	b.set("P.1", "deferred")
	b.add("U", "conflicted", 1, UnmergedLabel)
	b.set("U", "closed")
	b.add("B", "blocked by U", 1)
	b.link("B", "U", "blocks")

	c.Ticket = "P"
	n := checkNothing(t, c, b)
	want := &NothingToRun{Scope: "P", NotDone: []NotDone{{"P.1", "deferred"}},
		Done: "READY_EMPTY after 0 tickets: nothing ready; SCOPE_OPEN: P: 1 of its 1 subticket not done: P.1 (deferred)"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("parent: got  %+v\nwant %+v", n, want)
	}

	c.Ticket = "B"
	why := "blocked by U outside the scope, closed but not merged"
	n = checkNothing(t, c, b)
	want = &NothingToRun{Scope: "B", NotDone: []NotDone{{"B", why}},
		Done: "READY_EMPTY after 0 tickets: nothing ready; " +
			"SCOPE_OPEN: B: its 0 subtickets are merged, but B itself is not done (" + why + ")"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("blocked: got  %+v\nwant %+v", n, want)
	}
}

// unshownBeads is a tracker whose bd show fails.
type unshownBeads struct {
	*fakeBeads
	err error
}

func (u unshownBeads) Show(_ context.Context, id string) (Ticket, error) {
	return Ticket{ID: id, Status: "unknown"}, u.err
}

// When bd can't show what blocks a ready ticket, the check can't say whether the loop would hold
// it: it returns bd's error, and the run goes ahead. Without a ticket closed but not merged, no
// blocker can hold one, and bd show isn't asked.
func TestNothingToRunGoesAheadWhenBdCantShowTheBlockers(t *testing.T) {
	t.Parallel()
	failed := errors.New("bd show: database locked")
	g := newFakeGit("main")
	b := newFakeBeads()
	b.add("A", "ready", 1)
	if n, err := CheckNothingToRun(context.Background(), nothingConfig(t, 0), unshownBeads{b, failed}, g, g); n != nil ||
		err != nil {
		t.Errorf("nothing unmerged: got %+v, %v; want something to run", n, err)
	}
	b.add("U", "conflicted", 1, UnmergedLabel)
	b.set("U", "closed")
	n, err := CheckNothingToRun(context.Background(), nothingConfig(t, 0), unshownBeads{b, failed}, g, g)
	if n != nil || !errors.Is(err, failed) {
		t.Errorf("U unmerged: got %+v, %v; want bd show's error", n, err)
	}
}

// What the check finds held, the loop holds: a run of the same tickets starts none of them and ends
// at once, each one's hold said.
func TestNothingToRunHoldsWhatTheLoopHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.ExcludeTypes = []string{"epic"}
		h.beads.add("U", "conflicted", 1, UnmergedLabel)
		h.beads.set("U", "closed")
		h.beads.add("B", "blocked by U", 1)
		h.beads.link("B", "U", "blocks")
		h.beads.add("P", "parent", 2)
		h.beads.sub("P.1", "P", "child", 1)
		h.beads.set("P.1", "deferred")
		n, err := CheckNothingToRun(t.Context(), h.cfg, h.beads, h.mem, h.mem)
		want := &NothingToRun{HeldParents: 1, HeldBlocked: 1, Deferred: 1, Unmerged: 1,
			Done: "READY_EMPTY after 0 tickets: nothing ready"}
		if err != nil || !reflect.DeepEqual(n, want) {
			t.Fatalf("got  %+v, %v\nwant %+v", n, err, want)
		}

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, said := range []string{"B waits: U closed but not merged (" + earlierRun + ")",
			"P waits: its subtickets are not all closed and merged"} {
			if !strings.Contains(ev, said) {
				t.Errorf("the run didn't say %q:\n%s", said, ev)
			}
		}
	})
}
