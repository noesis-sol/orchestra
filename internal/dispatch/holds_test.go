package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTickets stands in for Beads: a ready queue, and each ticket's dependencies for Show.
type fakeTickets struct {
	ready []Ticket
	shown map[string]Ticket
}

func (f fakeTickets) Ready(context.Context, string) ([]Ticket, error)       { return f.ready, nil }
func (f fakeTickets) Unclosed(ctx context.Context) ([]Ticket, error)        { return nil, nil }
func (f fakeTickets) Descendants(context.Context, string) ([]Ticket, error) { return nil, nil }
func (f fakeTickets) Show(ctx context.Context, id string) (Ticket, error) {
	if t, ok := f.shown[id]; ok {
		return t, nil
	}
	return Ticket{ID: id, Status: "unknown"}, fmt.Errorf("bd show %s: not found", id)
}
func (f fakeTickets) Status(ctx context.Context, id string) (string, error) {
	t, err := f.Show(ctx, id)
	return t.Status, err
}
func (f fakeTickets) Describe(ctx context.Context, id string) string { return id }
func (f fakeTickets) Closed(ctx context.Context, label string) ([]Ticket, error) {
	var closed []Ticket
	for _, t := range f.shown {
		if t.Status == "closed" && HasLabel(t, label) {
			closed = append(closed, t)
		}
	}
	return closed, nil
}

// aBlocksB is Beads with A closed (so bd ready lists B, which A blocks) and C ready on its own.
func aBlocksB() fakeTickets {
	b := Ticket{ID: "k-b", Status: "open", Dependencies: []Ticket{
		{ID: "k-x", Status: "open", DependencyType: "related"},
		{ID: "k-a", Status: "closed", DependencyType: "blocks"},
	}}
	return fakeTickets{ready: []Ticket{{ID: "k-b", Status: "open"}},
		shown: map[string]Ticket{"k-b": b, "k-c": {ID: "k-c", Status: "open"}}}
}

func (f *mergeFixture) next(t *testing.T, running map[string]bool) string {
	t.Helper()
	tk, _, s := f.orch.next(context.Background(), running)
	if s != nil {
		t.Fatal(s)
	}
	if tk == nil {
		return ""
	}
	return tk.ID
}

func TestDependentWaitsWhileItsBlockerIsInFlight(t *testing.T) {
	f := newMergeFixture(t, "true")
	tk := aBlocksB()
	tk.ready = append(tk.ready, Ticket{ID: "k-c", Status: "open"})
	f.orch.tickets = tk
	running := map[string]bool{"k-a": true} // closed, waiting in the merge queue
	if got := f.next(t, running); got != "k-c" {
		t.Errorf("next = %q, want k-c while k-a has not merged", got)
	}
	running["k-c"] = true
	if got := f.next(t, running); got != "" {
		t.Errorf("next = %q, want nothing", got)
	}
	if ev := f.sink.text(); strings.Count(ev, "k-b waits: waiting for k-a to merge") != 1 {
		t.Errorf("the wait should be said once; events:\n%s", ev)
	}
	delete(running, "k-a") // merged
	if got := f.next(t, running); got != "k-b" {
		t.Errorf("next = %q, want k-b once k-a merged", got)
	}
}

func TestDependentWaitsWhileItsBlockerIsUnmerged(t *testing.T) {
	cases := []struct {
		name  string
		check string
		setup func(f *mergeFixture) string // makes k-a's branch, returns its worktree
		why   string
	}{
		{"conflict", "true", func(f *mergeFixture) string {
			wt := f.ticket(t, "k-a", "shared.txt", "line 1 from the ticket\n")
			f.onMain(t, "shared.txt", "line 1 from main\n")
			return wt
		}, "MERGE_CONFLICT"},
		{"checks fail", "exit 3", func(f *mergeFixture) string {
			wt := f.ticket(t, "k-a", "a.txt", "a\n")
			f.onMain(t, "b.txt", "b\n")
			return wt
		}, "CHECKS_FAILED"},
		{"no commit", "true", func(f *mergeFixture) string {
			wt := filepath.Join(t.TempDir(), "k-a")
			f.git(f.repo, "worktree", "add", "-q", "-b", "wt/k-a", wt, "main")
			return wt
		}, "CLOSED_WITHOUT_COMMIT"},
		{"dirty", "true", func(f *mergeFixture) string {
			wt := f.ticket(t, "k-a", "a.txt", "a\n")
			if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("unfinished\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			return wt
		}, "CLOSED_WITHOUT_COMMIT"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newMergeFixture(t, c.check)
			f.orch.tickets = aBlocksB()
			wt := c.setup(f)
			if s := f.orch.finish(context.Background(), "k-a", "wt/k-a", wt, "tab"); s != nil {
				t.Fatal(s)
			}
			if got := f.next(t, nil); got != "" {
				t.Errorf("next = %q, want k-b held while k-a is unmerged", got)
			}
			if ev := f.sink.text(); !strings.Contains(ev, "k-b waits: k-a closed but not merged ("+c.why+")") {
				t.Errorf("events:\n%s", ev)
			}
		})
	}
}

func TestDependentStartsOnceItsBlockerMerges(t *testing.T) {
	f := newMergeFixture(t, "exit 3")
	f.orch.tickets = aBlocksB()
	wt := f.ticket(t, "k-a", "a.txt", "a\n")
	f.onMain(t, "b.txt", "b\n")
	if s := f.orch.finish(context.Background(), "k-a", "wt/k-a", wt, "tab"); s != nil { // CHECKS_FAILED
		t.Fatal(s)
	}
	if got := f.next(t, nil); got != "" {
		t.Fatalf("next = %q, want k-b held", got)
	}
	f.orch.cfg.Check = "true" // fixed by hand, and merged
	if s := f.orch.finish(context.Background(), "k-a", "wt/k-a", wt, "tab"); s != nil {
		t.Fatal(s)
	}
	if !strings.Contains(f.sink.text(), "k-a closed") {
		t.Fatalf("k-a did not merge; events:\n%s", f.sink.text())
	}
	if got := f.next(t, nil); got != "k-b" {
		t.Errorf("next = %q, want k-b once k-a merged", got)
	}
}

func TestDependentWaitsWhenItsDependenciesCannotBeRead(t *testing.T) {
	f := newMergeFixture(t, "true")
	f.orch.tickets = fakeTickets{ready: []Ticket{{ID: "k-b", Status: "open"}}}
	if got := f.next(t, map[string]bool{"k-a": true}); got != "" {
		t.Errorf("next = %q, want k-b held", got)
	}
	if got := f.next(t, nil); got != "k-b" {
		t.Errorf("next = %q, want k-b: with nothing running or unmerged there is nothing to wait for", got)
	}
}

// countingTickets counts bd show calls.
type countingTickets struct {
	fakeTickets
	shows map[string]int
}

func (c countingTickets) Show(ctx context.Context, id string) (Ticket, error) {
	c.shows[id]++
	return c.fakeTickets.Show(ctx, id)
}

func TestBlockersAreReadOncePerRun(t *testing.T) {
	f := newMergeFixture(t, "true")
	one, none := 1, 0
	tk := aBlocksB()
	tk.ready = []Ticket{{ID: "k-b", Status: "open", DependencyCount: &one}, {ID: "k-c", Status: "open", DependencyCount: &none}}
	counting := countingTickets{tk, map[string]int{}}
	f.orch.tickets = counting
	running := map[string]bool{"k-a": true}
	for range 3 {
		if got := f.next(t, running); got != "k-c" {
			t.Fatalf("next = %q, want k-c while k-a has not merged", got)
		}
	}
	if counting.shows["k-b"] != 1 || counting.shows["k-c"] != 1 {
		t.Errorf("bd show calls = %v, want one each", counting.shows)
	}

	// A new blocker changes bd ready's count, so k-b's blockers are read again.
	delete(running, "k-a") // merged
	running["k-d"] = true
	two := 2
	b := tk.shown["k-b"]
	b.Dependencies = append(b.Dependencies, Ticket{ID: "k-d", Status: "closed", DependencyType: "blocks"})
	tk.shown["k-b"] = b
	tk.ready[0].DependencyCount = &two
	if got := f.next(t, running); got != "k-c" {
		t.Errorf("next = %q, want k-c while k-d has not merged", got)
	}
	if counting.shows["k-b"] != 2 || counting.shows["k-c"] != 1 {
		t.Errorf("bd show calls = %v, want k-b read again", counting.shows)
	}
	if ev := f.sink.text(); !strings.Contains(ev, "k-b waits: waiting for k-d to merge") {
		t.Errorf("events:\n%s", ev)
	}
}

func TestBlockersAreReadEachTimeWithoutACount(t *testing.T) {
	f := newMergeFixture(t, "true")
	counting := countingTickets{aBlocksB(), map[string]int{}}
	f.orch.tickets = counting
	for range 2 {
		if got := f.next(t, map[string]bool{"k-a": true}); got != "" {
			t.Fatalf("next = %q, want k-b held", got)
		}
	}
	if counting.shows["k-b"] != 2 {
		t.Errorf("bd show calls = %v, want k-b read each time", counting.shows)
	}
}

// A ticket set aside in this run stays out of it, except one that waited on a question: bd lists
// it as ready again only once the question is answered, and then it comes back. Set aside again
// after that, it stays out.
func TestSetAsideTicketsStayOutUnlessTheirQuestionWasAnswered(t *testing.T) {
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	ready := readyTickets{{ID: "deferred"}, {ID: "answered"}, {ID: "fresh"}}
	o := New(Config{Repo: "repo", Base: "main"}, log, "", Deps{Tickets: ready, Checkout: cleanCheckout{}})
	o.SetSink(&recordSink{})
	o.markAside("deferred")
	o.markAside("answered")
	o.setAsked("answered", true)
	if tk, _, s := o.next(context.Background(), nil); s != nil || tk == nil || tk.ID != "answered" {
		t.Fatalf("got %v (%v), want the ticket whose question was answered", tk, s)
	}
	o.setAsked("answered", false) // dispatched again, then set aside for another reason
	if tk, _, s := o.next(context.Background(), nil); s != nil || tk == nil || tk.ID != "fresh" {
		t.Fatalf("got %v (%v), want the fresh ticket", tk, s)
	}
}

// labelledA is Beads with k-a closed and labelled unmerged by an earlier run, blocking k-b.
func labelledA() fakeTickets {
	tk := aBlocksB()
	tk.shown["k-a"] = Ticket{ID: "k-a", Status: "closed", Labels: []string{UnmergedLabel}}
	return tk
}

func TestUnmergedLabelStaysUntilACommitNamingTheTicketIsOnBase(t *testing.T) {
	f := newMergeFixture(t, "true")
	f.orch.tickets = labelledA()
	f.git(f.repo, "branch", "wt/k-a") // cut, never committed to
	if s := f.orch.loadUnmerged(context.Background()); s != nil {
		t.Fatal(s)
	}
	if got := f.next(t, nil); got != "" {
		t.Errorf("next = %q, want k-b held: wt/k-a is on main but holds no commit naming k-a", got)
	}

	f = newMergeFixture(t, "true")
	f.orch.tickets = labelledA()
	f.onMain(t, "a.txt", "a\n")
	f.git(f.repo, "commit", "-q", "--amend", "-m", "k-a: add a.txt") // merged by hand, branch deleted
	if s := f.orch.loadUnmerged(context.Background()); s != nil {
		t.Fatal(s)
	}
	if got := f.next(t, nil); got != "k-b" {
		t.Errorf("next = %q, want k-b: k-a is on main\n%s", got, f.sink.text())
	}
}

// closedUnreadable is Beads that can't list closed tickets.
type closedUnreadable struct{ readyTickets }

func (closedUnreadable) Closed(ctx context.Context, label string) ([]Ticket, error) {
	return nil, errBd
}

func TestRunStopsWhenTheUnmergedTicketsCannotBeListed(t *testing.T) {
	noLeaks(t)
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	o := New(Config{Repo: "repo", Base: "main", Limit: 10, Concurrency: 1}, log, "",
		Deps{Tickets: closedUnreadable{readyTickets{{ID: "A"}}}, Checkout: cleanCheckout{}})
	o.SetSink(&recordSink{})
	if code := o.Run(context.Background()); code != ExitTool {
		t.Errorf("exit code %d, want %d", code, ExitTool)
	}
	if !strings.HasPrefix(o.Final(), "READY_UNREADABLE: could not list the tickets labelled 'unmerged': bd defer A") {
		t.Errorf("final line %q", o.Final())
	}
}

func TestLabelFailureIsWarned(t *testing.T) {
	f := newMergeFixture(t, "true")
	f.orch.notes = brokenBd(nil)
	f.orch.tickets = aBlocksB()
	wt := filepath.Join(t.TempDir(), "k-a")
	f.git(f.repo, "worktree", "add", "-q", "-b", "wt/k-a", wt, "main")
	if s := f.orch.finish(context.Background(), "k-a", "wt/k-a", wt, "tab"); s != nil { // CLOSED_WITHOUT_COMMIT
		t.Fatal(s)
	}
	if ev := f.sink.text(); !strings.Contains(ev, "LABEL_FAILED: bd could not label k-a 'unmerged': bd defer A") {
		t.Errorf("events:\n%s", ev)
	}
	if got := f.next(t, nil); got != "" {
		t.Errorf("next = %q, want k-b held in this run all the same", got)
	}
}

func TestUnmergedTicketHoldsItsDependentsInLaterRuns(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.beads.link("B", "A", "blocks")
	h.worker("A", closesWithoutCommit)
	h.worker("B", finishes("b.txt"))
	if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if a, _ := h.beads.Show(context.Background(), "A"); !HasLabel(a, UnmergedLabel) {
		t.Fatalf("A should be labelled %q: %v", UnmergedLabel, a.Labels)
	}

	// Run 2: bd ready lists B, since A is closed, but A's code is still not on main.
	if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
		t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if ev := h.sink.text(); !strings.Contains(ev, "B waits: A closed but not merged (left unmerged by an earlier run)") {
		t.Errorf("events:\n%s", ev)
	}

	// The maintainer finishes A by hand, keeping its branch; run 3 sees it on main.
	wt := h.worktree("A")
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.git(wt, "add", "a.txt")
	h.git(wt, "commit", "-q", "-m", "A: add a.txt")
	h.git(h.repo, "merge", "-q", "--ff-only", "wt/A")
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("run 3: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if ev := h.sink.text(); !strings.Contains(ev, "A, left unmerged by an earlier run, is on main now") {
		t.Errorf("events:\n%s", ev)
	}
	if a, _ := h.beads.Show(context.Background(), "A"); HasLabel(a, UnmergedLabel) {
		t.Errorf("A's label should be removed: %v", a.Labels)
	}
	if log := h.mainLog(); !strings.Contains(log, "B: add b.txt") {
		t.Errorf("main:\n%s", log)
	}
}

func TestReopenedUnmergedTicketLosesItsLabelWhenItMerges(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.beads.link("B", "A", "blocks")
	h.worker("A", closesWithoutCommit, finishes("a.txt"))
	h.worker("B", finishes("b.txt"))
	if o, code := h.run(); code != ExitOK {
		t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	h.beads.set("A", "open") // the maintainer reopens it
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if a, _ := h.beads.Show(context.Background(), "A"); HasLabel(a, UnmergedLabel) {
		t.Errorf("A merged, so its label should be removed: %v", a.Labels)
	}
	if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || !strings.Contains(log, "B: add b.txt") {
		t.Errorf("main:\n%s", log)
	}
}
