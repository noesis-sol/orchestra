package dispatch

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// A ticket closed where the run didn't see it close, its work still only on its branch, holds the
// tickets it blocks, and those filed from it, until its branch is on Base: as a ticket left unmerged
// does, with one CLOSED_UNMERGED warning.

// cutBranch makes branch br from the base's commits, as git branch does.
func cutBranch(g *fakeGit, br string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.branches[br] = g.resolve(g.base)
}

// branchAsks is git in memory that notes the commit the base was on each time a branch's existence
// is asked.
type branchAsks struct {
	*fakeGit
	mu    sync.Mutex
	asked map[string][]string
}

func (b *branchAsks) HasBranch(ctx context.Context, repo, branch string) (bool, error) {
	head := b.head(b.base)
	b.mu.Lock()
	b.asked[branch] = append(b.asked[branch], head)
	b.mu.Unlock()
	return b.fakeGit.HasBranch(ctx, repo, branch)
}

// heads is the commits the base was on as branch was asked of, in order.
func (b *branchAsks) heads(branch string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.asked[branch])
}

// closedUnmergedWarning is the warning for ticket A, closed with wt/A not on main.
const closedUnmergedWarning = "A   CLOSED_UNMERGED: A is closed, but wt/A is not on main: the tickets it blocks wait " +
	"until it merges; merge it into main, or delete the branch if its work isn't wanted (git branch -D wt/A)"

// A was closed by hand, never dispatched, with its commit only on wt/A. B, which A blocks, and F,
// filed from A, wait for it, the holds saying why, with one warning. C's worker merges wt/A into main
// meanwhile, by hand: B and F then start, on a base with A's code. Git is asked of wt/A once for each
// commit main is on, however many tickets A holds.
func TestClosedTicketWithItsBranchOffBaseHoldsItsDependents(t *testing.T) {
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
		h.beads.add("F", "filed from A", 3)
		h.beads.link("F", "A", "discovered-from")
		h.worker("C", func(w *fakeWorker) AgentState {
			time.Sleep(2 * time.Minute) // B and F are held meanwhile, poll after poll
			if out, err := w.git.FastForward(context.Background(), "", "wt/A"); err != nil {
				t.Errorf("merging wt/A: %v: %s", err, out)
			}
			time.Sleep(time.Minute)
			return finishes("c.txt")(w)
		})
		h.worker("B", cutWith("A: add a.txt", "b.txt"))
		h.worker("F", cutWith("A: add a.txt", "f.txt"))
		o := h.loop()
		asks := &branchAsks{fakeGit: h.mem, asked: map[string][]string{}}
		o.worktrees = asks
		if code := o.Run(t.Context()); code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.dispatched(); !equal(got, []string{"C", "B", "F"}) {
			t.Errorf("dispatched %v, want C, then B and F once wt/A merged", got)
		}
		if got := closedIDs(h); !equal(slices.Sorted(slices.Values(got)), []string{"B", "C", "F"}) {
			t.Errorf("merged %v, want B, C and F", got)
		}
		ev := h.sink.text()
		for _, want := range []string{
			"  B waits: A closed but not merged (wt/A is not on main)",
			"  F waits: filed by A's worker; A closed but not merged (wt/A is not on main)",
		} {
			if strings.Count(ev, want) != 1 {
				t.Errorf("events say %q %d times, want once:\n%s", want, strings.Count(ev, want), ev)
			}
		}
		var warned []string
		for _, w := range h.sink.eventsOf(EvWarn) {
			if tagOf(w) == "CLOSED_UNMERGED" {
				warned = append(warned, strings.TrimSpace(w.Ticket+" "+w.Text))
			}
		}
		if !equal(warned, []string{closedUnmergedWarning}) {
			t.Errorf("CLOSED_UNMERGED warnings %q, want one:\n%s", warned, closedUnmergedWarning)
		}
		// Once with main where it began, through several polls, then once for each commit it moved to
		// while a ticket waited on A: with wt/A merged into it, and with B merged as F waited for a slot.
		heads := asks.heads("wt/A")
		if len(heads) < 2 || len(slices.Compact(slices.Clone(heads))) != len(heads) {
			t.Errorf("git asked of wt/A with main on %v, want once for each commit main was on", heads)
		}
	})
}

// B started with no link to A; its worker finds B needs A, closed with wt/A not on main, links it
// and stops with nothing done. B goes back in the queue, open, not counted toward the hold for the
// environment, and waits for A.
func TestWorkerFindingAClosedBlockerOffBaseWaitsForIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.EnvHoldCount, h.cfg.EnvHoldWindow = 1, time.Hour // one worker failing at once would hold the run
		h.beads.add("A", "closed by hand", 1)
		h.beads.set("A", StatusClosed)
		cutBranch(h.mem, "wt/A")
		h.mem.commit("wt/A", "A: add a.txt", "a.txt")
		h.beads.add("B", "needs A", 2)
		h.worker("B", needs("A", StatusOpen))
		o, code := h.run()
		if code != ExitOK || !strings.HasPrefix(o.Final(), "READY_EMPTY") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		waits := h.sink.of(EvWaits)
		if len(waits) != 1 || !strings.HasPrefix(waits[0], "B   WAITS_FOR_MERGE: B waits for A to merge; ") {
			t.Errorf("waits events %q, want one for B, waiting for A", waits)
		}
		ev := h.sink.text()
		for _, not := range []string{"deferred", "ENVIRONMENT"} {
			if strings.Contains(ev, not) {
				t.Errorf("events say %s:\n%s", not, ev)
			}
		}
		if !strings.Contains(ev, "  B waits: A closed but not merged (wt/A is not on main)") {
			t.Errorf("B isn't held for A:\n%s", ev)
		}
		if n := strings.Count(ev, "CLOSED_UNMERGED"); n != 1 {
			t.Errorf("%d CLOSED_UNMERGED warnings, want one:\n%s", n, ev)
		}
		if st := h.statusOf("B"); st != StatusOpen {
			t.Errorf("B is %s, want open", st)
		}
		if aside := o.asideList(); len(aside) != 0 {
			t.Errorf("set aside: %v", aside)
		}
	})
}

// A closed blocker with no branch, with a branch holding nothing beyond main, or with one main holds
// the changes of (squash-merged), holds nothing: B starts at once, without a warning.
func TestClosedBlockerWithNothingOffBaseHoldsNothing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		branch func(g *fakeGit)
	}{
		{"no branch", func(*fakeGit) {}},
		{"nothing beyond main", func(g *fakeGit) { cutBranch(g, "wt/A") }},
		{"squash-merged", func(g *fakeGit) {
			cutBranch(g, "wt/A")
			g.commit("wt/A", "A: add a.txt", "a.txt")
			g.commit("main", "A: add a.txt (squashed)", "a.txt")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.beads.add("A", "closed", 1)
				h.beads.set("A", StatusClosed)
				tc.branch(h.mem)
				h.beads.add("B", "blocked by A", 2)
				h.beads.link("B", "A", "blocks")
				h.worker("B", finishes("b.txt"))
				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if strings.Contains(h.sink.text(), "waits") || h.sink.warned("CLOSED_UNMERGED", "") {
					t.Errorf("B held, or A warned of:\n%s", h.sink.text())
				}
			})
		})
	}
}

// The check for nothing to run counts a ticket held by a closed blocker off main as waiting for it
// to merge; once wt/A is on main, B is one to run.
func TestNothingToRunCountsATicketHeldByAClosedBlockerOffBase(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	b := newFakeBeads()
	b.add("A", "closed by hand", 1)
	b.set("A", StatusClosed)
	b.add("B", "blocked by A", 2)
	b.link("B", "A", "blocks")
	g := newFakeGit(c.Base)
	cutBranch(g, "wt/A")
	g.commit("wt/A", "A: add a.txt", "a.txt")
	n := checkNothingOn(t, c, b, g)
	want := &NothingToRun{HeldBlocked: 1, Done: "READY_EMPTY after 0 tickets: nothing ready"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("got  %+v\nwant %+v", n, want)
	}
	if out, err := g.FastForward(context.Background(), c.Repo, "wt/A"); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if n := checkNothingOn(t, c, b, g); n != nil {
		t.Errorf("wt/A merged: got %+v, want B to run", n)
	}
}

// A source off main holds its follow-up only while bd shows it closed: one open or deferred, not
// running, may never run, and holds nothing, whatever its branch holds.
func TestClosedSourceOffBaseHoldsItsFollowUp(t *testing.T) {
	t.Parallel()
	off := func(_ context.Context, id string) string { return branchOf(id) + " is not on main" }
	for _, tc := range []struct {
		name string
		l    links
		want string
	}{
		{"blocker", links{blockers: []string{"A"}}, "A closed but not merged (wt/A is not on main)"},
		{"closed source", links{sources: []string{"A"}, closed: map[string]bool{"A": true}},
			"filed by A's worker; A closed but not merged (wt/A is not on main)"},
		{"open source", links{sources: []string{"A"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := holds{unmerged: func(string) string { return "" }, branches: off,
				links: func(context.Context, Ticket) (links, error) { return tc.l, nil }}
			if got, err := h.why(context.Background(), Ticket{ID: "B"}); got.why != tc.want || err != nil {
				t.Errorf("why = %q, %v; want %q", got.why, err, tc.want)
			}
		})
	}
}
