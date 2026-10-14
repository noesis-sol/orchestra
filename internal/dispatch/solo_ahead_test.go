package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A solo ticket goes ahead of what would otherwise take a slot beside it: a recheck, when it is
// next in line, and new tickets, when it runs, however it came to run.

// orchestra-74b2.9: with every slot taken, a poll read the queue and forgot that solo ticket S was
// next in line, so the recheck that fell due as a slot freed took it ahead of S. Here A's check
// fails until B lands the fix; B merges while C still runs, with S first in line. S starts once C
// is done, alone, and A is checked again only after S.
func TestASoloTicketNextInLineGoesAheadOfARecheck(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		dir := t.TempDir()
		h.cfg.Check = countedCheck(dir, "")
		h.beads.add("A", "first", 1)
		h.beads.add("B", "the fix", 2)
		h.beads.add("C", "slow", 3)
		h.beads.add("S", "split the loop", 4, SoloLabel)
		h.worker("A", landsMeanwhile("a.txt"))
		h.worker("B", func(w *fakeWorker) AgentState {
			time.Sleep(2 * readyPoll) // polls with every slot taken and S first in line
			w.write(dir, "fixed", "")
			return finishes("b.txt")(w)
		})
		h.worker("C", busyFor(4*readyPoll, "c.txt"))
		h.worker("S", finishes("s.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 4 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"B", "C", "S", "A"}) {
			t.Errorf("merged %v, want B, C, then S, and A checked again after S\n%s", got, h.sink.text())
		}
		ev := h.sink.text()
		if s, re := strings.Index(ev, "S dispatching solo"), strings.Index(ev, "RECHECK: A"); s < 0 || re < s {
			t.Errorf("want S dispatched before A is checked again:\n%s", ev)
		}
	})
}

// orchestra-74b2.9: a solo ticket whose worker claimed it again in its tab after its question was
// adopted, but ran beside the tickets that started after it, as only a dispatched one was known to
// run alone. Here S asks; B, C and D start while it waits, B answers, and S's worker carries on. E,
// ready behind them, waits for S to finish rather than taking a slot B or D leaves.
func TestASoloTicketAdoptedAfterItsQuestionRunsAlone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.Concurrency = 3
		h.beads.add("S", "split the loop", 1, SoloLabel)
		h.beads.add("B", "second", 2)
		h.beads.add("C", "third", 3)
		h.beads.add("D", "fourth", 4)
		h.beads.add("E", "fifth", 5)
		answered := make(chan struct{})
		var v overlap
		h.worker("S", asksThenCarriesOn(hooks, answered, v.runs("S", func(w *fakeWorker) AgentState {
			w.shows("working")
			hooks.report(w.wt, "PreToolUse")
			w.claim()
			time.Sleep(10 * time.Minute) // B and D finish meanwhile
			w.commit("s.txt")
			w.close()
			hooks.report(w.wt, "Stop")
			return "idle"
		})))
		h.worker("B", answersAfter(time.Minute, answered, 2*time.Minute, "b.txt"))
		h.worker("C", busyFor(time.Hour, "c.txt"))
		h.worker("D", busyFor(4*time.Minute, "d.txt"))
		h.worker("E", v.runs("E", finishes("e.txt")))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 5 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.of(EvAnswered); len(got) != 1 || !strings.Contains(got[0], "so its worker is adopted") {
			t.Fatalf("answered %v, want S adopted in its tab\n%s", got, h.sink.text())
		}
		if got := v.of("E"); len(got) != 0 {
			t.Errorf("E ran beside %v, want it started after S, adopted solo, finished\n%s", got, h.sink.text())
		}
		if got := closedIDs(h); len(got) != 5 || !equal(got[:4], []string{"B", "D", "S", "E"}) {
			t.Errorf("merged %v, want B and D, then S, then E\n%s", got, h.sink.text())
		}
	})
}
