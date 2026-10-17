package dispatch

import (
	"context"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A ticket set aside with its worker left in its tab is followed: closed there later, it is merged
// (LATE_CLOSE), and the tickets it blocks wait for that merge.

// leavesOpen is a worker that settles with its ticket still open, once startGrace has passed, which
// sets it aside with its tab left open.
func leavesOpen(w *fakeWorker) AgentState {
	time.Sleep(time.Minute)
	return "idle"
}

// closesInTab is A's worker, left in tab, carrying on there: it commits on A's branch and closes A.
func closesInTab(h *harness, id, file string) {
	h.t.Helper()
	if err := h.mem.commitIn(h.worktree(id), id+": add "+file, file); err != nil {
		h.t.Error(err)
	}
	h.beads.set(id, StatusClosed)
}

// eventIndex is the index of the first event of kind k for ticket id, or -1.
func eventIndex(h *harness, k Kind, id string) int {
	h.sink.mu.Lock()
	defer h.sink.mu.Unlock()
	return slices.IndexFunc(h.sink.events, func(ev Event) bool { return ev.Kind == k && ev.Ticket == id })
}

// A's worker settles with A open: A is set aside, its tab left open. C's worker closes A in A's tab
// (as A's worker would, carrying on there) and finishes C, which merges and moves main on. B, which
// A blocks, is ready then, but waits; on the next poll A is merged as a late close: rebased onto
// main, checked and merged, its worktree, branch and tab removed. B starts after that, on a main
// with A's code.
func TestTicketClosedInItsLeftTabIsMergedAndHoldsItsDependents(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.cfg.Check = "true"
		h.beads.add("A", "left open", 1)
		h.beads.add("C", "closes A", 2)
		h.beads.add("D", "keeps the run going", 3)
		h.beads.add("B", "blocked by A", 1)
		h.beads.link("B", "A", "blocks")
		h.worker("A", leavesOpen)
		h.worker("C", func(w *fakeWorker) AgentState {
			// A is set aside meanwhile. C merges a few seconds after closing A, before the next ready poll.
			time.Sleep(startGrace + 2*time.Minute + 10*time.Second)
			closesInTab(h, "A", "a.txt")
			return finishes("c.txt")(w)
		})
		h.worker("D", busyFor(startGrace+5*time.Minute, "d.txt"))
		h.worker("B", cutWith("A: add a.txt", "b.txt"))

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 4 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		tab := tabOf(h, "A")
		ev := h.sink.text()
		for _, want := range []string{
			"A still open -> noted and deferred; worktree " + h.worktree("A") + " and tab " + tab + " left open",
			"  B waits: A closed but not merged (its worker was left in tab " + tab + ")",
			"  LATE_CLOSE: A was closed in tab " + tab + " after it was set aside; merging it now",
		} {
			if strings.Count(ev, want) != 1 {
				t.Errorf("events say %q %d times, want once:\n%s", want, strings.Count(ev, want), ev)
			}
		}
		if log := h.logged(); !strings.Contains(log, "rebased wt/A onto main, which moved on while it ran") ||
			!strings.Contains(log, "'true' passes on the rebased wt/A") {
			t.Errorf("A was not rebased onto main and checked before its merge:\n%s", log)
		}
		if got := closedIDs(h); !equal(got, []string{"C", "A", "D", "B"}) && !equal(got, []string{"C", "A", "B", "D"}) {
			t.Errorf("merged %v, want C, then A, then B and D", got)
		}
		if merged, started := eventIndex(h, EvClosed, "A"), eventIndex(h, EvDispatch, "B"); started < merged {
			t.Errorf("B dispatched (event %d) before A merged (event %d):\n%s", started, merged, ev)
		}
		if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") {
			t.Errorf("main lacks A's commit:\n%s", log)
		}
		if exists(h.worktree("A")) || slices.Contains(h.mem.branchList(), "wt/A") ||
			!slices.Contains(h.herdr.tabsClosed(), tab) {
			t.Errorf("A's worktree, branch wt/A and tab %s should be removed; branches %v, tabs closed %v",
				tab, h.mem.branchList(), h.herdr.tabsClosed())
		}
		if a, _ := h.beads.Show(t.Context(), "A"); HasLabel(a, UnmergedLabel) {
			t.Errorf("A, merged, still has its '%s' label", UnmergedLabel)
		}
		if slices.Contains(o.asideList(), "A") || len(o.leftOpenList()) != 0 {
			t.Errorf("A, merged, is still set aside (%v) or followed (%v)", o.asideList(), o.leftOpenList())
		}
	})
}

// A's worker settles with A open, and the run ends before A's worker closes it: A carries the
// unmerged label and the commit wt/A was cut from. Closed after the run, it holds B in the next run
// until wt/A is merged into main.
func TestTicketClosedInItsLeftTabAfterTheRunHoldsItsDependentsInTheNext(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "left open", 1)
		h.beads.add("B", "blocked by A", 2)
		h.beads.link("B", "A", "blocks")
		h.worker("A", leavesOpen)
		h.worker("B", cutWith("A: add a.txt", "b.txt"))
		fork := h.mem.Head(t.Context(), "", "main")

		if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if a, _ := h.beads.Show(t.Context(), "A"); !HasLabel(a, UnmergedLabel) {
			t.Errorf("A, left in its tab, lacks the '%s' label: %v", UnmergedLabel, a.Labels)
		}
		if got := h.beads.metadata("A", ForkKey); got != fork {
			t.Errorf("A's %s is %q, want %q, where wt/A was cut", ForkKey, got, fork)
		}

		closesInTab(h, "A", "a.txt") // once the run has ended
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.dispatched(); !equal(got, []string{"A"}) {
			t.Errorf("dispatched %v by run 2, want B held for A", got)
		}
		if want := "  A was left unmerged by an earlier run; tickets it blocks wait until wt/A is merged into main"; !strings.Contains(h.sink.text(), want) {
			t.Errorf("run 2 events lack %q:\n%s", want, h.sink.text())
		}

		if out, err := h.mem.FastForward(context.Background(), "", "wt/A"); err != nil {
			t.Fatalf("merging wt/A by hand: %v: %s", err, out)
		}
		if o, code := h.run(); code != ExitOK || !strings.HasPrefix(o.Final(), "READY_EMPTY after 1 tickets") {
			t.Fatalf("run 3: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.dispatched(); !equal(got, []string{"A", "B"}) {
			t.Errorf("dispatched %v, want B once wt/A merged", got)
		}
	})
}

// A's worker settles with A open, and its tab is closed: A is no longer followed, and closed later
// it isn't merged as a late close.
func TestLeftOpenWorkerWhoseTabIsGoneIsNoLongerFollowed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "left open", 1)
		h.beads.add("C", "beside", 2)
		h.worker("A", leavesOpen)
		h.worker("C", func(w *fakeWorker) AgentState {
			time.Sleep(startGrace + 2*time.Minute) // A is set aside meanwhile
			if err := h.herdr.CloseTab(context.Background(), tabOf(h, "A")); err != nil {
				t.Error(err)
			}
			time.Sleep(2 * time.Minute)
			closesInTab(h, "A", "a.txt")
			time.Sleep(2 * time.Minute)
			return finishes("c.txt")(w)
		})

		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		if want := "  A's worker, left in tab " + tabOf(h, "A") + " as A was set aside, is gone with it deferred; " +
			"it is no longer followed"; !strings.Contains(ev, want) {
			t.Errorf("events lack %q:\n%s", want, ev)
		}
		if strings.Contains(ev, "LATE_CLOSE") || slices.Contains(closedIDs(h), "A") {
			t.Errorf("A, its worker gone, was merged as a late close:\n%s", ev)
		}
		if got := o.leftOpenList(); len(got) != 0 {
			t.Errorf("still followed: %v", got)
		}
	})
}

// A and B fail at once and are set aside, their tabs left open; the hold for the environment
// reopens them, and A, dispatched again, is no longer followed as left open: its new worker carries
// it. B, not dispatched again, still is, its worker idle in its tab.
func TestLeftOpenTicketDispatchedAgainIsNoLongerFollowed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		h.cfg.EnvProbe = time.Minute
		h.cfg.Limit = 3
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.worker("A", givesUp, finishes("a.txt"))
		h.worker("B", givesUp)
		h.worker(probeID, runsProbe)

		o, code := h.run()
		if code != ExitOK || o.Final() != "LIMIT_REACHED at 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.logged())
		}
		var followed []string
		for _, lo := range o.leftOpenList() {
			followed = append(followed, lo.w.id)
		}
		if !equal(followed, []string{"B"}) {
			t.Errorf("followed as left open %v, want B alone", followed)
		}
		if a, _ := h.beads.Show(t.Context(), "A"); HasLabel(a, UnmergedLabel) {
			t.Errorf("A, merged, still has its '%s' label", UnmergedLabel)
		}
	})
}
