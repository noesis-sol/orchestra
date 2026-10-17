package dispatch

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// A worker's tab is left open only while it holds something to review: a ticket dispatched again
// has its earlier worker's tab closed, as the new worker takes up its worktree, and a worker set
// aside with nothing done has its worktree, branch and tab removed.

// openTabsOf is how many tabs the terminal has open labelled id.
func openTabsOf(h *harness, id string) int {
	h.herdr.mu.Lock()
	defer h.herdr.mu.Unlock()
	n := 0
	for _, label := range h.herdr.labels {
		if label == id {
			n++
		}
	}
	return n
}

// A, with an earlier attempt on its branch, fails at once and is set aside, its worker left idle in
// its tab; so does B, and the hold for the environment reopens both. A, dispatched again, has its
// earlier worker's tab closed rather than renamed, the log naming that worker's transcript, and its
// new worker has the only tab labelled A.
func TestTicketDispatchedAgainClosesItsEarlierWorkersTab(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		h.cfg.EnvProbe = time.Minute
		h.cfg.Limit = 3
		withSession(h, "A")
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		earlierAttempt(h, "A")
		earlierAttempt(h, "B")
		tabs := -1
		h.worker("A", givesUp, func(w *fakeWorker) AgentState {
			tabs = openTabsOf(h, "A")
			return finishes("a.txt")(w)
		})
		h.worker("B", givesUp)
		h.worker(probeID, runsProbe)

		o, code := h.run()
		if code != ExitOK || o.Final() != "LIMIT_REACHED at 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.logged())
		}
		if got := dispatches(h, "A"); got != 2 {
			t.Fatalf("A dispatched %d times, want twice:\n%s", got, h.sink.text())
		}
		ev := h.sink.text()
		h.herdr.mu.Lock()
		var earlier string
		for _, p := range h.herdr.panes {
			if p.ticket == "A" && (earlier == "" || p.tab < earlier) {
				earlier = p.tab
			}
		}
		h.herdr.mu.Unlock()
		if !slices.Contains(h.herdr.tabsClosed(), earlier) {
			t.Errorf("A's earlier tab %s was not closed; tabs closed %v", earlier, h.herdr.tabsClosed())
		}
		if strings.Contains(ev, "renamed to") {
			t.Errorf("A's earlier worker was renamed, not closed:\n%s", ev)
		}
		want := "  A's earlier worker was idle in tab " + earlier + "; its tab is closed, as the new worker takes up " +
			"its worktree; its session's transcript is /home/u/.claude/projects/x/" + testSession + ".jsonl"
		if !strings.Contains(h.logged(), want) {
			t.Errorf("the log lacks %q:\n%s", want, h.logged())
		}
		if tabs != 1 {
			t.Errorf("A's new worker ran beside %d tabs labelled A, want its own alone", tabs)
		}
	})
}

// screenWhileOpen is the terminal, noting for each ticket whose worker's screen is read whether
// the worker's tab was still open then.
type screenWhileOpen struct {
	Agents
	h    *harness
	mu   sync.Mutex
	open map[string]bool
}

func (s *screenWhileOpen) Screen(ctx context.Context, name string, st AgentState) string {
	open := openTabsOf(s.h, name) > 0
	s.mu.Lock()
	s.open[name] = open
	s.mu.Unlock()
	return s.Agents.Screen(ctx, name, st)
}

// A's worker defers A, and B's settles with B still open, each with nothing done: each is set aside
// with its worktree, branch and tab removed, once triage has its evidence, and isn't followed as
// left open.
func TestWorkerSetAsideWithNothingDoneLeavesNoTab(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "defers", 1)
		h.beads.add("B", "stays open", 2)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.deferIt()
			return "idle"
		})
		h.worker("B", func(w *fakeWorker) AgentState {
			time.Sleep(time.Minute)
			return "idle"
		})
		o := h.loop()
		screens := &screenWhileOpen{Agents: o.agents, h: h, open: map[string]bool{}}
		o.agents = screens
		o.StartTriage() // without claude: each deferral's evidence is gathered, and its triage fails
		code := o.Run(t.Context())
		o.FinishTriage(t.Context())

		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"  A deferred by worker; nothing done, so its worktree, branch and tab are removed",
			"  B still open -> noted and deferred; nothing done, so its worktree, branch and tab are removed",
		} {
			if strings.Count(ev, want) != 1 {
				t.Errorf("events say %q %d times, want once:\n%s", want, strings.Count(ev, want), ev)
			}
		}
		for _, id := range []string{"A", "B"} {
			if exists(h.worktree(id)) || slices.Contains(h.mem.branchList(), branchOf(id)) ||
				!slices.Contains(h.herdr.tabsClosed(), tabOf(h, id)) {
				t.Errorf("%s's worktree, branch and tab %s should be removed; branches %v, tabs closed %v",
					id, tabOf(h, id), h.mem.branchList(), h.herdr.tabsClosed())
			}
			if open, read := screens.open[id]; !read || !open {
				t.Errorf("%s's screen read for triage: %v, its tab open then: %v; want read before its tab closed",
					id, read, open)
			}
			if a, _ := h.beads.Show(t.Context(), id); HasLabel(a, UnmergedLabel) {
				t.Errorf("%s, with nothing done, has the '%s' label", id, UnmergedLabel)
			}
		}
		if got := o.leftOpenList(); len(got) != 0 {
			t.Errorf("followed as left open: %v, want none", got)
		}
		if got := h.statusOf("B"); got != StatusDeferred {
			t.Errorf("B is %s, want deferred", got)
		}
	})
}

// A's worker commits and defers A, and B's leaves a change uncommitted with B still open: each
// keeps its worktree, branch and tab for review, and is followed as left open.
func TestWorkerSetAsideWithWorkKeepsItsTab(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "defers", 1)
		h.beads.add("B", "stays open", 2)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.commit("a.txt")
			w.deferIt()
			return "idle"
		})
		h.worker("B", func(w *fakeWorker) AgentState {
			time.Sleep(time.Minute)
			h.mem.leaveChange(w.wt, "b.txt")
			return "idle"
		})

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"  A deferred by worker; worktree " + h.worktree("A") + " and tab " + tabOf(h, "A") + " left open",
			"  B still open -> noted and deferred; worktree " + h.worktree("B") + " and tab " + tabOf(h, "B") +
				" left open",
		} {
			if strings.Count(ev, want) != 1 {
				t.Errorf("events say %q %d times, want once:\n%s", want, strings.Count(ev, want), ev)
			}
		}
		if strings.Contains(ev, "nothing done") {
			t.Errorf("a worker with work was taken for one with nothing done:\n%s", ev)
		}
		for _, id := range []string{"A", "B"} {
			if !exists(h.worktree(id)) || !slices.Contains(h.mem.branchList(), branchOf(id)) ||
				slices.Contains(h.herdr.tabsClosed(), tabOf(h, id)) {
				t.Errorf("%s's worktree, branch and tab %s should be kept; branches %v, tabs closed %v",
					id, tabOf(h, id), h.mem.branchList(), h.herdr.tabsClosed())
			}
		}
		var followed []string
		for _, lo := range o.leftOpenList() {
			followed = append(followed, lo.w.id)
		}
		if !equal(followed, []string{"A", "B"}) {
			t.Errorf("followed as left open %v, want A and B", followed)
		}
	})
}

// With real git, A's worker defers A with nothing done: its worktree and branch go.
func TestWorkerDeferringWithNothingDoneHasItsBranchDeleted(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "defers", 1)
	h.worker("A", func(w *fakeWorker) AgentState { w.claim(); w.deferIt(); return "idle" })

	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if want := "  A deferred by worker; nothing done, so its worktree, branch and tab are removed"; !strings.Contains(h.sink.text(), want) {
		t.Errorf("events lack %q:\n%s", want, h.sink.text())
	}
	if exists(h.worktree("A")) || strings.Contains(h.git(h.repo, "branch", "--list", "wt/A"), "wt/A") ||
		!slices.Contains(h.herdr.tabsClosed(), tabOf(h, "A")) {
		t.Errorf("A's worktree, branch and tab should be removed; tabs closed %v\n%s", h.herdr.tabsClosed(), h.logged())
	}
}
