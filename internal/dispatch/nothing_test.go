package dispatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// The check for nothing to run, before any Loop: what it reads, how it classifies what is left,
// and that it writes nothing.

// nothingConfig is a run of all of bd ready in a repository of its own, epics excluded, with done
// tickets done so far.
func nothingConfig(t *testing.T, done int) Config {
	return Config{Repo: t.TempDir(), Base: "main", Limit: 10, DoneSoFar: done, ExcludeTypes: []string{"epic"}}
}

// checkNothing runs the check on b, with git holding only c.Base's first commit, as checkNothingOn
// does.
func checkNothing(t *testing.T, c Config, b *fakeBeads) *NothingToRun {
	t.Helper()
	return checkNothingOn(t, c, b, newFakeGit(c.Base))
}

// checkNothingOn runs the check on b and g, failing the test on an error or on anything it wrote:
// the tickets, their labels and notes stay as they were, git's branches too, and no run files
// appear.
func checkNothingOn(t *testing.T, c Config, b *fakeBeads, g *fakeGit) *NothingToRun {
	t.Helper()
	snapshot := func() map[string]Ticket {
		b.mu.Lock()
		defer b.mu.Unlock()
		m := map[string]Ticket{}
		for id, tk := range b.tickets {
			cp := *tk
			cp.Labels = append([]string(nil), tk.Labels...)
			m[id] = cp
		}
		return m
	}
	branches := func() map[string]string {
		m := map[string]string{}
		for _, br := range g.branchList() {
			m[br] = g.log(br)
		}
		return m
	}
	before, beforeGit := snapshot(), branches()
	n, err := CheckNothingToRun(context.Background(), c, b, g, g)
	if err != nil {
		t.Fatal(err)
	}
	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Errorf("the check changed the tickets:\nbefore %+v\nafter  %+v", before, after)
	}
	if after := branches(); !reflect.DeepEqual(beforeGit, after) {
		t.Errorf("the check changed git:\nbefore %v\nafter  %v", beforeGit, after)
	}
	b.mu.Lock()
	notes := len(b.notes)
	b.mu.Unlock()
	if notes > 0 {
		t.Errorf("the check wrote notes: %v", b.notes)
	}
	if _, err := os.Stat(filepath.Join(c.Repo, ".orchestra")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the check wrote run files: %v", err)
	}
	return n
}

// carry leaves workers on the tickets in the repository's state, as a run that ended does.
func carry(t *testing.T, repo string, ids ...string) {
	t.Helper()
	var left []project.LeftWorker
	for _, id := range ids {
		left = append(left, project.LeftWorker{Ticket: id, Tab: "t1", Worktree: "/wt/" + id, Question: "Q"})
	}
	if err := project.SaveState(repo, project.RunState{Workers: left}); err != nil {
		t.Fatal(err)
	}
}

// A run has something to run when bd ready lists a ticket, or the last run left a worker; only
// when neither does is there nothing.
func TestNothingToRunOnlyWithNothingReadyAndNothingCarried(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	b := newFakeBeads()
	b.add("A", "ready", 1)
	if n := checkNothing(t, c, b); n != nil {
		t.Errorf("A is ready, yet nothing to run: %+v", n)
	}
	b.set("A", "in_progress")
	if n := checkNothing(t, c, b); n == nil {
		t.Fatal("nothing ready and nothing carried, yet something to run")
	}
	carry(t, c.Repo, "A")
	g := newFakeGit(c.Base)
	if n, err := CheckNothingToRun(context.Background(), c, b, g, g); n != nil || err != nil {
		t.Errorf("a worker carried over on A, yet nothing to run: %+v %v", n, err)
	}
}

// In a scoped run, only a worker carried over on the scope or one of its descendants counts.
func TestNothingToRunCarriedInTheScopeOnly(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	c.Ticket = "R"
	b := newFakeBeads()
	b.add("R", "root", 1)
	b.sub("R.1", "R", "child", 1)
	b.sub("R.1.1", "R.1", "grandchild", 1)
	b.add("X", "elsewhere", 1)
	for _, id := range []string{"R", "R.1", "R.1.1", "X"} {
		b.set(id, "in_progress")
	}
	carry(t, c.Repo, "X")
	g := newFakeGit(c.Base)
	if n, err := CheckNothingToRun(context.Background(), c, b, g, g); n == nil || err != nil {
		t.Errorf("the worker on X is outside R's scope, yet something to run: %v", err)
	}
	for _, id := range []string{"R", "R.1.1"} {
		carry(t, c.Repo, "X", id)
		if n, err := CheckNothingToRun(context.Background(), c, b, g, g); n != nil || err != nil {
			t.Errorf("a worker carried over on %s, in R's scope, yet nothing to run: %+v %v", id, n, err)
		}
	}
}

// unreadyBeads is a tracker whose bd ready fails.
type unreadyBeads struct {
	*fakeBeads
	err error
}

func (u unreadyBeads) Ready(context.Context, string) ([]Ticket, error) { return nil, u.err }

// When bd ready fails, the check returns its error, and the run goes ahead to report it.
func TestNothingToRunReturnsReadysError(t *testing.T) {
	t.Parallel()
	failed := errors.New("bd ready: database locked")
	g := newFakeGit("main")
	n, err := CheckNothingToRun(context.Background(), nothingConfig(t, 0), unreadyBeads{newFakeBeads(), failed}, g, g)
	if n != nil || !errors.Is(err, failed) {
		t.Errorf("got %+v, %v; want bd ready's error", n, err)
	}
}

// With no ticket left but epics, which never run, and none closed but unmerged, everything is
// done; the open epics are listed for the maintainer to close.
func TestNothingToRunAllDone(t *testing.T) {
	t.Parallel()
	b := newFakeBeads()
	n := checkNothing(t, nothingConfig(t, 0), b)
	want := &NothingToRun{AllDone: true, Done: "READY_EMPTY after 0 tickets: everything is done"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("empty tracker: got %+v, want %+v", n, want)
	}

	b.add("E", "epic", 1)
	b.kind("E", "epic")
	b.sub("E.1", "E", "done", 1)
	b.set("E.1", "closed")
	b.add("F", "another epic", 2)
	b.kind("F", "epic")
	b.add("D", "done long ago", 2)
	b.set("D", "closed")
	n = checkNothing(t, nothingConfig(t, 4), b)
	want = &NothingToRun{AllDone: true, StillOpen: []string{"E", "F"},
		Done: "READY_EMPTY after 4 tickets: everything is done"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("open epics: got %+v, want %+v", n, want)
	}
}

// A ticket closed but not merged is work left: nothing is ready rather than all done.
func TestNothingToRunUnmergedIsNotAllDone(t *testing.T) {
	t.Parallel()
	b := newFakeBeads()
	b.add("E", "epic", 1)
	b.kind("E", "epic")
	b.add("U", "conflicted", 1, UnmergedLabel)
	b.set("U", "closed")
	n := checkNothing(t, nothingConfig(t, 0), b)
	want := &NothingToRun{Unmerged: 1, Done: "READY_EMPTY after 0 tickets: nothing ready"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("got %+v, want %+v", n, want)
	}
}

// Nothing ready counts the tickets left by what holds them; epics don't count.
func TestNothingToRunCountsWhatHoldsTheTickets(t *testing.T) {
	t.Parallel()
	b := newFakeBeads()
	b.add("Q", "which way?", 1, HumanLabel)
	b.add("Q2", "answered later", 1, HumanLabel)
	b.set("Q2", "in_progress")
	b.add("A", "asks", 1)
	b.link("A", "Q", "blocks")
	b.add("B", "blocked by A", 2)
	b.link("B", "A", "blocks")
	b.add("S", "marked blocked", 2)
	b.set("S", "blocked")
	b.add("P", "in progress", 2)
	b.set("P", "in_progress")
	b.add("D", "deferred", 2)
	b.set("D", "deferred")
	b.add("H", "hooked", 3)
	b.set("H", "hooked")
	b.add("U", "conflicted", 1, UnmergedLabel)
	b.set("U", "closed")
	b.add("V", "checks failed", 1, UnmergedLabel)
	b.set("V", "closed")
	b.add("E", "epic", 1)
	b.kind("E", "epic")
	n := checkNothing(t, nothingConfig(t, 2), b)
	want := &NothingToRun{Questions: 2, Waiting: 3, InProgress: 1, Deferred: 1, Other: 1, Unmerged: 2,
		Done: "READY_EMPTY after 2 tickets: nothing ready"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("got  %+v\nwant %+v", n, want)
	}
	if n.Open() != 8 {
		t.Errorf("open %d, want 8", n.Open())
	}
}

// A scoped run with nothing ready names each ticket of its scope not done with SCOPE_OPEN's
// reasons, and its done line ends with the SCOPE_OPEN a run would end with.
func TestNothingToRunScopedSaysWhyEachIsNotDone(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	c.Ticket = "E"
	b := newFakeBeads()
	b.add("B", "blocker outside", 0)
	b.set("B", "in_progress")
	b.add("Q", "which way?", 1, HumanLabel)
	b.add("E", "epic", 1)
	b.kind("E", "epic")
	b.sub("E.1", "E", "blocked outside", 1)
	b.link("E.1", "B", "blocks")
	b.sub("E.2", "E", "in progress", 1)
	b.set("E.2", "in_progress")
	b.sub("E.3", "E", "deferred", 1)
	b.set("E.3", "deferred")
	b.sub("E.4", "E", "asks", 1)
	b.link("E.4", "Q", "blocks")
	b.sub("E.5", "E", "blocked inside", 1)
	b.link("E.5", "E.2", "blocks")
	b.sub("E.6", "E", "conflicted", 1, UnmergedLabel)
	b.set("E.6", "closed")
	b.sub("E.7", "E", "merged", 1)
	b.set("E.7", "closed")
	n := checkNothing(t, c, b)
	scopeOpen := "; SCOPE_OPEN: E: 6 of its 7 subtickets not done: E.1 (blocked by B outside the scope), " +
		"E.2 (in progress), E.3 (deferred), E.4 (waiting on your answer to Q), E.5 (blocked by E.2), " +
		"E.6 (closed but not merged: left unmerged by an earlier run)"
	want := &NothingToRun{Scope: "E", Done: "READY_EMPTY after 0 tickets: nothing ready" + scopeOpen, NotDone: []NotDone{
		{"E.1", "blocked by B outside the scope"}, {"E.2", "in progress"}, {"E.3", "deferred"},
		{"E.4", "waiting on your answer to Q"}, {"E.5", "blocked by E.2"},
		{"E.6", "closed but not merged: left unmerged by an earlier run"}}}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("got  %+v\nwant %+v", n, want)
	}

	// A run that had left E.6 unmerged ends with the same SCOPE_OPEN.
	o := New(c, nil, "", Deps{Tickets: b})
	o.unmerged = map[string]string{"E.6": earlierRun}
	subs, err := b.Descendants(context.Background(), "E")
	if got := o.scopeEnd(context.Background(), subs, err); got != scopeOpen {
		t.Errorf("scopeEnd %q\n   check %q", got, scopeOpen)
	}
}

// A scope's own ticket left undone, once its subtickets are merged, is the one not done.
func TestNothingToRunScopedTicketItselfNotDone(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 1)
	c.Ticket = "A"
	b := newFakeBeads()
	b.add("A", "alone", 1)
	b.set("A", "in_progress")
	n := checkNothing(t, c, b)
	want := &NothingToRun{Scope: "A", NotDone: []NotDone{{"A", "in progress"}},
		Done: "READY_EMPTY after 1 tickets: nothing ready; " +
			"SCOPE_OPEN: A: its 0 subtickets are merged, but A itself is not done (in progress)"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("got  %+v\nwant %+v", n, want)
	}
}

// An epic scope whose subtickets are all closed and merged is all done, the epic left open for the
// maintainer to close; a ticket closed and merged with its subtickets is simply done.
func TestNothingToRunScopeDone(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	c.Ticket = "E"
	b := newFakeBeads()
	b.add("E", "epic", 1)
	b.kind("E", "epic")
	b.sub("E.1", "E", "first", 1)
	b.sub("E.2", "E", "second", 1)
	b.set("E.1", "closed")
	b.set("E.2", "closed")
	b.add("U", "unmerged elsewhere", 1, UnmergedLabel) // outside the scope: it doesn't hold E
	b.set("U", "closed")
	n := checkNothing(t, c, b)
	want := &NothingToRun{Scope: "E", AllDone: true, StillOpen: []string{"E"},
		Done: "READY_EMPTY after 0 tickets: everything is done; " +
			"SCOPE_DONE: E's 2 subtickets are merged; close it with: bd close E"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("epic: got  %+v\nwant %+v", n, want)
	}

	b.set("E.2", "open")
	b.link("E.2", "U", "blocks") // closed, so bd ready lists E.2: there is something to run
	if n := checkNothing(t, c, b); n != nil {
		t.Errorf("E.2 is ready, yet nothing to run: %+v", n)
	}

	c.Ticket = "R"
	b.add("R", "root", 1)
	b.sub("R.1", "R", "child", 1)
	b.set("R.1", "closed")
	b.set("R", "closed")
	n = checkNothing(t, c, b)
	want = &NothingToRun{Scope: "R", AllDone: true,
		Done: "READY_EMPTY after 0 tickets: everything is done; SCOPE_DONE: R and its 1 subticket are merged"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("ticket: got  %+v\nwant %+v", n, want)
	}
}
