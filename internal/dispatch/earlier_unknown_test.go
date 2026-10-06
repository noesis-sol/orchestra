package dispatch

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// showAs has Herdr show the agent named name as st from now on, or no longer have it if st is
// StateGone.
func (h *fakeHerdr) showAs(name string, st AgentState) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if st == StateGone {
		h.agents = slices.DeleteFunc(h.agents, func(a *fakeAgent) bool { return a.name == name })
		return
	}
	h.agent(name).status = st
}

// An earlier worker Herdr keeps showing as unknown, on a ticket that was not asked in this run, may
// still be at work: the run stops for it, as for one working, before its branch is touched or a
// second worker started.
func TestAnEarlierWorkerStayingUnknownStopsTheRunBeforeItsWorktree(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.herdr.agents = append(h.herdr.agents, &fakeAgent{name: "A", kind: "claude", pane: "elsewhere", status: "unknown"})
		o, code := h.run()
		want := "AGENT_BUSY: an earlier worker for A is still in its tab, though Herdr can't tell what it is doing"
		if code != ExitTool || !strings.HasPrefix(o.Final(), want) {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.herdr.argsFor("A"); len(got) != 0 {
			t.Errorf("a worker was started beside the earlier one: %v", got)
		}
		if slices.Contains(h.mem.branchList(), branchOf("A")) {
			t.Error("A's branch was made under the earlier worker")
		}
		if ev := h.sink.text(); strings.Contains(ev, "renamed") {
			t.Errorf("the earlier worker was renamed:\n%s", ev)
		}
	})
}

// An earlier worker Herdr shows as unknown for a moment, then idle or gone, is renamed or left, as
// any other that is not at work, and a new worker takes the ticket.
func TestAnEarlierWorkerUnknownForAMomentIsPassedOver(t *testing.T) {
	t.Parallel()
	for _, then := range []AgentState{StateIdle, StateGone} {
		t.Run(string(then), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.beads.add("A", "first", 1)
				h.worker("A", finishes("a.txt"))
				h.herdr.agents = append(h.herdr.agents,
					&fakeAgent{name: "A", kind: "claude", pane: "elsewhere", status: "unknown"})
				go func() {
					time.Sleep(2 * statusPoll) // read once more as unknown
					h.herdr.showAs("A", then)
				}()
				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if got := closedIDs(h); !equal(got, []string{"A"}) || len(h.herdr.argsFor("A")) != 1 {
					t.Errorf("merged %v with %d workers started; want A from one new worker\n%s",
						got, len(h.herdr.argsFor("A")), h.sink.text())
				}
				renamed := strings.Contains(h.sink.text(), "earlier worker for A renamed to ")
				if renamed != (then == StateIdle) {
					t.Errorf("earlier worker renamed: %v, for one turning %s\n%s", renamed, then, h.sink.text())
				}
			})
		})
	}
}

// An asked ticket back through bd ready whose earlier worker Herdr keeps showing as unknown has
// that worker adopted, as one still working is, rather than its branch rebased and a second one
// started beside it.
func TestAskedTicketWhoseWorkerStaysUnknownIsAdopted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		answered := make(chan struct{})
		h.worker("A", asksThenCarriesOn(hooks, answered, func(w *fakeWorker) AgentState {
			w.shows("unknown")
			hooks.report(w.wt, "PreToolUse")
			time.Sleep(3 * time.Minute) // the next run starts meanwhile
			w.claim()
			w.commit("a.txt")
			w.close()
			hooks.report(w.wt, "Stop")
			return "idle"
		}))
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		h.beads.set("Q", "closed")
		close(answered)
		synctest.Wait() // the worker is at work again, though Herdr can't tell

		o, code := h.run()
		if code != ExitOK || !strings.HasPrefix(o.Final(), "READY_EMPTY after ") {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		want := "  A's earlier worker is still in tab tab1, though Herdr can't tell what it is doing; " +
			"adopting it rather than starting another"
		if !strings.Contains(ev, want) || strings.Contains(ev, "renamed") {
			t.Errorf("A's worker should be adopted, not renamed:\n%s", ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) || len(h.herdr.argsFor("A")) != 1 {
			t.Errorf("merged %v, A's worker started %d times; want A merged from its one worker",
				got, len(h.herdr.argsFor("A")))
		}
	})
}
