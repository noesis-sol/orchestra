package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// The launch contract (Starter): a terminal refuses to launch a worker into a pane where a program
// still runs, rather than type into it. A launch from the prompt file refused so is not taken for a
// worker on its way: the worker is started plainly, its prompt pasted, once the pane is free.
func TestALaunchRefusedWhileThePaneIsBusyStartsTheWorkerPlainly(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.herdr.paneBusy = map[string]int{"A": 1}
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		got := h.herdr.argsFor("A")
		if len(got) != 2 || got[0][0] != "launch" || got[1][0] != "start" {
			t.Errorf("launched and started as %q; want the refused launch, then one start", got)
		}
		if got := h.herdr.pastedTo(); !equal(got, []string{"A"}) {
			t.Errorf("pasted to %v, want A", got)
		}
		logged := h.logged()
		for _, want := range []string{"could not be started from its prompt file",
			"herdr pane run pane1: make still runs there; nothing is typed into it"} {
			if !strings.Contains(logged, want) {
				t.Errorf("log lacks %q:\n%s", want, logged)
			}
		}
	})
}

// A pane that stays busy gets no worker at all: the start stops once its tries are spent, and nothing
// was typed into what runs there.
func TestAPaneThatStaysBusyGetsNoWorker(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.herdr.paneBusy = map[string]int{"A": 100}
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		o, code := h.run()
		if want := "START_FAILED for A in tab tab1"; code != ExitTool || !strings.HasPrefix(o.Final(), want) {
			t.Fatalf("exit %d, final %q, want it to start %q\n%s", code, o.Final(), want, h.sink.text())
		}
		h.herdr.mu.Lock()
		defer h.herdr.mu.Unlock()
		if a := h.herdr.inPane("pane1"); a != nil {
			t.Errorf("an agent was started in the busy pane: %+v", a)
		}
		if left := h.herdr.paneBusy["A"]; left != 100-11 {
			t.Errorf("%d launches and starts were tried, want the launch and 10 starts", 100-left)
		}
	})
}
