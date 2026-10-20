package dispatch

import (
	"regexp"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// An earlier worker waiting on the usage limit when its ticket is dispatched again carries on by
// itself, or told to, after the reset: it is adopted, as one at work is, rather than a second worker
// started in its worktree beside it.

// A's worker asks a question; the maintainer answers it in the worker's tab, where it reaches the
// usage limit. A, back once the question is closed, has that worker adopted: told to carry on after
// the reset, it finishes A from its one tab.
func TestAskedTicketWhoseWorkerIsLimitedIsAdopted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.Concurrency = 2
		h.cfg.UsageLimitWait = 6 * time.Hour
		reset := time.Now().Add(2 * time.Hour)
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		answered, limited := make(chan struct{}), make(chan struct{})
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.ask("Q", "which way?")
			hooks.report(w.wt, "Stop")
			w.shows("idle") // asked, and stopped
			<-answered
			hooks.limit(w.wt, reset)
			w.shows(StateLimited)
			close(limited)
			return StateLimited
		}, carriesOn(hooks, "a.txt", time.Minute))
		h.worker("B", func(w *fakeWorker) AgentState {
			for len(h.sink.eventsOf(EvAsked)) == 0 { // A is set aside, asked
				time.Sleep(time.Second)
			}
			close(answered)
			<-limited
			w.beads.set("Q", "closed")
			return finishes("b.txt")(w)
		})
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		if !regexp.MustCompile(`A's earlier worker is still limited in tab tab\d; adopting it rather than starting another`).
			MatchString(ev) {
			t.Errorf("A's worker was not adopted:\n%s", ev)
		}
		if strings.Contains(ev, "renamed") {
			t.Errorf("A's earlier worker was renamed:\n%s", ev)
		}
		if got := h.herdr.argsFor("A"); len(got) != 1 {
			t.Errorf("A's worker started %d times, want once: %v", len(got), got)
		}
		if got := h.herdr.pastedText("A"); len(got) != 1 || !strings.Contains(got[0], "usage limit has reset. Carry on with A") {
			t.Errorf("pasted to A: %q, want it told to carry on after the reset", got)
		}
		if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || !strings.Contains(log, "B: add b.txt") {
			t.Errorf("main:\n%s", log)
		}
	})
}

// A worker an earlier run left in its tab, no record of this run's, that waits on the usage limit
// when its ticket is dispatched again is found under the ticket's name and adopted.
func TestEarlierRunsLimitedWorkerIsAdopted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.UsageLimitWait = 6 * time.Hour
		h.beads.add("A", "a", 1)
		var wt string
		h.worker("A", func(w *fakeWorker) AgentState {
			wt = w.wt
			w.claim()
			w.commit("a.txt")
			w.deferIt()
			return "idle"
		}, carriesOn(hooks, "b.txt", time.Minute))
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		earlier := tabOf(h, "A")
		h.beads.set("A", "open") // bd undefer A
		// Told to carry on by hand since, the worker reached the usage limit.
		hooks.limit(wt, time.Now().Add(2*time.Hour))
		h.herdr.showAs("A", StateLimited)

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		want := "A's earlier worker is still limited in tab " + earlier + "; adopting it rather than starting another"
		if !strings.Contains(ev, want) {
			t.Errorf("events lack %q:\n%s", want, ev)
		}
		if got := h.herdr.argsFor("A"); len(got) != 1 {
			t.Errorf("A's worker started %d times, want once: %v", len(got), got)
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v, want A", got)
		}
	})
}
