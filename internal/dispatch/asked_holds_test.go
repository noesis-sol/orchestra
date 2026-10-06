package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// cutWith is a worker that checks its branch was cut with the commit subject on it, then finishes
// its ticket.
func cutWith(subject, file string) behaviour {
	return func(w *fakeWorker) AgentState {
		if log := w.git.log(branchOf(w.id)); !strings.Contains(log, subject) {
			w.t.Errorf("%s started on a branch without %q:\n%s", w.id, subject, log)
		}
		return finishes(file)(w)
	}
}

// A asks; B is blocked by A, and P is A's parent. The maintainer answers in A's tab, and A's worker
// closes A there; before the next poll adopts it, C returns and the run looks for tickets to start.
// bd ready lists B and P then, A being closed, but they wait for A as they would for a running
// ticket: they start once it has merged, on a Base with its code.
func TestTicketsWaitingOnAnAskedTicketClosedInItsTabStartAfterItMerges(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.Concurrency = 2
		h.beads.add("P", "ticket P", 3)
		h.beads.sub("A", "P", "ticket A", 1)
		h.beads.add("C", "ticket C", 2)
		h.beads.add("B", "ticket B", 3)
		h.beads.link("B", "A", "blocks")
		answered := make(chan struct{})
		h.worker("A", asksThenCarriesOn(hooks, answered, func(w *fakeWorker) AgentState {
			w.shows("working")
			hooks.report(w.wt, "PreToolUse")
			w.claim()
			w.commit("a.txt")
			w.close()
			hooks.report(w.wt, "Stop")
			return "idle"
		}))
		// Answered between two polls; C returns soon after A's worker closes A, before the next.
		h.worker("C", answersAfter(5*time.Minute+5*time.Second, answered, 5*time.Second, "c.txt"))
		h.worker("B", cutWith("A: add a.txt", "b.txt"))
		h.worker("P", cutWith("A: add a.txt", "p.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 4 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); len(got) != 4 || !equal(got[:2], []string{"C", "A"}) {
			t.Errorf("merged %v; want C, then A, then B and P\n%s", got, h.sink.text())
		}
		if ev := h.sink.text(); !strings.Contains(ev, "B waits: waiting for A to merge") {
			t.Errorf("events lack B waiting for A:\n%s", ev)
		}
	})
}
