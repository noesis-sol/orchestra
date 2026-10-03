package dispatch

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// The check for nothing to run counts a ticket labelled unmerged as merged once git finds it merged
// since, by hand, as the loop's loadUnmerged does; it leaves the label for the loop to remove.

// A ticket an earlier run left unmerged, merged by hand since, is merged: with nothing else left
// but epics, all is done, and its label stays until a run reaches the loop.
func TestNothingToRunCountsATicketMergedByHandAsMerged(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	b := newFakeBeads()
	b.add("E", "epic", 1)
	b.kind("E", "epic")
	b.sub("E.1", "E", "conflicted, then merged by hand", 1, UnmergedLabel)
	b.set("E.1", "closed")
	g := newFakeGit(c.Base)
	g.commit(c.Base, "E.1: add a.txt", "a.txt") // its branch deleted
	n := checkNothingOn(t, c, b, g)
	want := &NothingToRun{AllDone: true, StillOpen: []string{"E"}, Done: "READY_EMPTY after 0 tickets: everything is done"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("got  %+v\nwant %+v", n, want)
	}
	if e1, _ := b.Show(context.Background(), "E.1"); !HasLabel(e1, UnmergedLabel) {
		t.Errorf("the check removed E.1's label: %v", e1.Labels)
	}

	// Its branch, kept, holds a commit naming it that isn't on the base: it is not merged.
	g = newFakeGit(c.Base)
	g.commit(c.Base, "E.1: add a.txt", "a.txt")
	g.commit("wt/E.1", "E.1: add a.txt again", "a.txt")
	n = checkNothingOn(t, c, b, g)
	want = &NothingToRun{Unmerged: 1, Done: "READY_EMPTY after 0 tickets: nothing ready"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("branch not on main: got  %+v\nwant %+v", n, want)
	}
}

// A scoped run whose subticket, left unmerged, has merged since by hand is done, as a run's
// SCOPE_DONE would say; until it has, the subticket is closed but not merged.
func TestNothingToRunScopedCountsATicketMergedByHandAsMerged(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	c.Ticket = "E"
	b := newFakeBeads()
	b.add("E", "epic", 1)
	b.kind("E", "epic")
	b.sub("E.1", "E", "conflicted", 1, UnmergedLabel)
	b.set("E.1", "closed")
	b.sub("E.2", "E", "merged", 1)
	b.set("E.2", "closed")
	g := newFakeGit(c.Base)
	n := checkNothingOn(t, c, b, g)
	want := &NothingToRun{Scope: "E", NotDone: []NotDone{{"E.1", "closed but not merged: " + earlierRun}},
		Done: "READY_EMPTY after 0 tickets: nothing ready; SCOPE_OPEN: E: 1 of its 2 subtickets not done: " +
			"E.1 (closed but not merged: " + earlierRun + ")"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("not merged: got  %+v\nwant %+v", n, want)
	}

	g.commit(c.Base, "E.1: add a.txt", "a.txt")
	n = checkNothingOn(t, c, b, g)
	want = &NothingToRun{Scope: "E", AllDone: true, StillOpen: []string{"E"},
		Done: "READY_EMPTY after 0 tickets: everything is done; " +
			"SCOPE_DONE: E's 2 subtickets are merged; close it with: bd close E"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("merged by hand: got  %+v\nwant %+v", n, want)
	}
}

// The check and the loop agree on which labelled tickets are merged: those the loop still holds as
// unmerged are those the check counts, before the loop removes the others' labels and after.
func TestNothingToRunAgreesWithTheLoopOnWhatIsMerged(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := nothingConfig(t, 0)
	b := newFakeBeads()
	g := newFakeGit(c.Base)
	wt := t.TempDir()
	for _, id := range []string{"M", "F", "B", "C"} {
		b.add(id, "left unmerged", 1, UnmergedLabel)
		b.set(id, "closed")
	}
	g.commit(c.Base, "M: add m.txt", "m.txt") // merged by hand, its branch deleted
	if _, err := g.NewWorktree(ctx, c.Repo, filepath.Join(wt, "F"), "wt/F", c.Base); err != nil {
		t.Fatal(err)
	}
	g.commit("wt/F", "F: add f.txt", "f.txt")
	if _, err := g.FastForward(ctx, c.Repo, "wt/F"); err != nil { // merged by hand, its branch kept
		t.Fatal(err)
	}
	g.commit("wt/B", "B: add b.txt", "b.txt") // not on main
	if _, err := g.NewWorktree(ctx, c.Repo, filepath.Join(wt, "C"), "wt/C", c.Base); err != nil {
		t.Fatal(err) // cut, never committed to
	}

	want := &NothingToRun{Unmerged: 2, Done: "READY_EMPTY after 0 tickets: nothing ready"}
	if n := checkNothingOn(t, c, b, g); !reflect.DeepEqual(n, want) {
		t.Errorf("before the loop: got  %+v\nwant %+v", n, want)
	}

	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	o := New(c, log, "", Deps{Tickets: b, Notes: b, Worktrees: g, Merger: g})
	o.SetSink(&recordSink{})
	if s := o.loadUnmerged(ctx); s != nil {
		t.Fatal(s)
	}
	var held []string
	for _, id := range []string{"M", "F", "B", "C"} {
		if o.unmergedWhy(id) != "" {
			held = append(held, id)
		}
	}
	if !slices.Equal(held, []string{"B", "C"}) {
		t.Errorf("the loop holds %v as unmerged, the check counted B and C", held)
	}
	closed, err := b.Closed(ctx, UnmergedLabel)
	if err != nil {
		t.Fatal(err)
	}
	var labelled []string
	for _, tk := range closed {
		labelled = append(labelled, tk.ID)
	}
	if !slices.Equal(labelled, []string{"B", "C"}) {
		t.Errorf("labelled after the loop: %v, want B and C", labelled)
	}
	if n := checkNothingOn(t, c, b, g); !reflect.DeepEqual(n, want) {
		t.Errorf("after the loop: got  %+v\nwant %+v", n, want)
	}
}
