package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// An asked ticket comes back and its earlier worker, idle in its tab, is told the answer is in, but
// the paste doesn't submit, and the worker starts on it only just after Herdr has given up waiting:
// the run reads it again and adopts it, working, rather than renaming it and starting a second
// worker on its branch.
func TestAskedWorkerStartingLateOnBeingToldIsAdopted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A",
			func(w *fakeWorker) AgentState {
				w.claim()
				w.ask("Q", "which way?")
				h.herdr.mu.Lock()
				h.herdr.enterLate["A"] = true // telling it the answer is in goes wrong
				h.herdr.mu.Unlock()
				return "idle"
			},
			finishes("a.txt")) // once told the answer is in
		h.worker("B", func(w *fakeWorker) AgentState {
			w.beads.set("Q", "closed") // the maintainer answers meanwhile
			return finishes("b.txt")(w)
		})
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		if !strings.Contains(ev, "A's earlier worker in tab tab1 is working after being told Q is answered; "+
			"adopting it rather than starting another") || strings.Contains(ev, "renamed") {
			t.Errorf("the earlier worker should have been adopted, not replaced:\n%s", ev)
		}
		if got := h.herdr.startsFor(); strings.Count(strings.Join(got, " "), "A") > 1 {
			t.Errorf("workers started for %v; want one for A", got)
		}
		if got := closedIDs(h); !equal(got, []string{"B", "A"}) {
			t.Errorf("merged %v", got)
		}
		if got := h.herdr.tabsClosed(); !equal(got, []string{"tab2", "tab1"}) {
			t.Errorf("tabs closed: %v; A's work should be merged from tab1, with no third tab\n%s", got, ev)
		}
	})
}
