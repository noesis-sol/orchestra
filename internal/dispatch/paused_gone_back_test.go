package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// orchestra-nr1v.4: a worker taken for gone a second time in a run stops it (PAUSED), its session
// resumed once already. In tutti's run of 2026-10-18 it was still in its tab, unnamed, and the next
// run, finding it there idle, left it waiting for the maintainer for 17 hours, though the PAUSED line
// said the next run would resume it. Back in its tab and idle, it is told to carry on, and adopted.
func TestWorkerGoneAgainAndBackInItsTabIsToldToCarryOnByTheNextRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.worker("A", vanishes, vanishes, finishes("a.txt"))
		o, code := h.run()
		want := "PAUSED: A still in_progress, its worker gone from tab tab2 (worktree " + h.worktree("A") +
			"); its session " + testSession + " was resumed once in this run already: the next run resumes it again, " +
			"or tells it to carry on should it be back in its tab; stopping so it can be looked at"
		if code != ExitStuck || o.Final() != want {
			t.Fatalf("run 1: exit %d, final %q\nwant %q\n%s", code, o.Final(), want, h.sink.text())
		}

		// The resumed worker is back in its tab, idle and unnamed, as Herdr showed it in tutti's run.
		h.herdr.mu.Lock()
		var back *fakeAgent
		for _, a := range h.herdr.agents {
			if p := h.herdr.panes[a.pane]; p.ticket == "A" && p.tab == "tab2" {
				back = a
			}
		}
		if back == nil {
			h.herdr.mu.Unlock()
			t.Fatalf("no worker of A's in tab2 to bring back")
		}
		back.name, back.status = "", StateIdle
		h.herdr.mu.Unlock()

		o, code = h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" { // adopted, not dispatched
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v; want A", got)
		}
		if starts := h.herdr.argsFor("A"); len(starts) != 2 {
			t.Errorf("started %d workers on A; want run 1's two, none in run 2: %q", len(starts), starts)
		}
		texts := h.herdr.pastedText("A")
		if len(texts) == 0 || !strings.HasPrefix(texts[len(texts)-1], "Orchestra: A is yours again.") {
			t.Errorf("A's worker back in its tab should be told to carry on; told %q", texts)
		}
		ev := h.sink.text()
		for _, line := range []string{
			"  A's worker in tab tab2 came up without its name; named it A\n",
			"  A, which the last run left running (PAUSED), is back and idle in tab tab2, so its worker is adopted\n",
			"  A's worker in tab tab2 was told to carry on and carries on; adopting it\n",
		} {
			if !strings.Contains(ev, line) {
				t.Errorf("run 2's events lack %q:\n%s", line, ev)
			}
		}
	})
}

// A worker the last run left running as it went idle with its ticket in progress waits on the
// maintainer, who is to answer it: the next run, finding it still idle, isn't to tell it to carry on.
func TestWorkerLeftIdleIsNotToldToCarryOnByTheNextRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return StateIdle })
		o, code := h.run()
		if code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: A still in_progress in tab tab1") {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		told := len(h.herdr.pastedText("A"))

		o, code = h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if texts := h.herdr.pastedText("A"); len(texts) != told {
			t.Errorf("A's idle worker was told %q in run 2; want nothing", texts[told:])
		}
	})
}
