package dispatch

import (
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A worker whose start Ctrl+C cut short before Herdr saw it comes up without its name, which the
// next run looks for it by. That run, finding no agent under the name, looks in the pane the worker
// was started in, as the state file has it, and names the unnamed worker there, while Herdr still
// has the tab labelled with the ticket's ID.

// paneName is the name of the agent in pane ("" for none, or one unnamed), read past Herdr, so an
// agent it is late to see stays unseen.
func paneName(h *fakeHerdr, pane string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if a := h.inPane(pane); a != nil {
		return a.name
	}
	return ""
}

// lateAgain has Herdr miss the agent in pane once more, as when the next run starts before Herdr
// has recognised it.
func lateAgain(t *testing.T, h *fakeHerdr, pane string) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	a := h.inPane(pane)
	if a == nil {
		t.Fatalf("no agent in %s", pane)
	}
	a.late = true
}

// cutUnnamed runs the loop with Herdr late to see A's worker, and presses Ctrl+C as Herdr begins to
// name it, so the run stops with the worker unnamed in pane1. The worker claims A claim after its
// launch, closing claimed, and commits and closes it 10 minutes later.
func cutUnnamed(t *testing.T, h *harness, claim time.Duration) (claimed <-chan struct{}) {
	t.Helper()
	h.beads.add("A", "first", 1)
	h.herdr.launchSlow["A"] = true
	c := make(chan struct{})
	h.worker("A", func(w *fakeWorker) AgentState {
		time.Sleep(claim)
		w.claim()
		close(c)
		time.Sleep(10 * time.Minute)
		w.commit("a.txt")
		w.close()
		return "idle"
	})
	if o, code := runCutAtNaming(t, h); code != ExitInterrupted {
		t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got, want := saved(t, h), savedCut(h); !reflect.DeepEqual(got, want) {
		t.Fatalf("saved %+v\nwant %+v", got, want)
	}
	if want := "A's start was cut short before Herdr saw a worker in tab tab1 to name A"; !strings.Contains(h.logged(), want) {
		t.Fatalf("log lacks %q:\n%s", want, h.logged())
	}
	if name := paneName(h.herdr, "pane1"); name != "" {
		t.Fatalf("A's worker is named %q; want it unnamed", name)
	}
	return c
}

// orchestra-6jh: the worker claimed its ticket after the run stopped. The next run names it from its
// pane as it loads it, adopts it rather than dropping it as gone (WORKER_GONE), and merges its ticket.
func TestUnnamedWorkerIsNamedFromItsPaneAndMergedByTheNextRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		<-cutUnnamed(t, h, 0) // in progress, as the next run finds it

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"  A's worker in tab tab1 came up without its name; named it A",
			"  carried over from the last run: A (left running: INTERRUPTED)",
			"  A, which the last run left running (INTERRUPTED), is at work again in tab tab1, so its worker is adopted",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("run 2 events lack %q:\n%s", want, ev)
			}
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) || len(h.herdr.argsFor("A")) != 1 {
			t.Errorf("merged %v, A's worker started %d times; want A merged from its one worker",
				got, len(h.herdr.argsFor("A")))
		}
		if got := warnings(h, "A"); len(got) != 0 {
			t.Errorf("A warned about:\n%s", strings.Join(got, "\n"))
		}
		if got := saved(t, h); len(got) != 0 {
			t.Errorf("run 2 saved %+v; want nothing left", got)
		}
	})
}

// The worker hadn't claimed its ticket when the next run loaded it, nor had Herdr seen it yet: the
// run names it as the ticket comes back through bd ready, and adopts it rather than starting a
// second worker on the same worktree.
func TestUnnamedWorkerIsNamedFromItsPaneAsItsTicketComesBack(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		cutUnnamed(t, h, 5*time.Minute)
		lateAgain(t, h.herdr, "pane1")

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"  A's worker in tab tab1 came up without its name; named it A",
			"  A's earlier worker is still working in tab tab1; adopting it rather than starting another",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("run 2 events lack %q:\n%s", want, ev)
			}
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) || len(h.herdr.argsFor("A")) != 1 {
			t.Errorf("merged %v, A's worker started %d times; want A merged from its one worker",
				got, len(h.herdr.argsFor("A")))
		}
	})
}

// The worker claimed its ticket while the next run had no slot free for it, Herdr having seen it
// only after the run loaded it: the run names it as it follows the ticket, and adopts it.
func TestUnnamedWorkerIsNamedFromItsPaneAsItClaimsItsTicket(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		cutUnnamed(t, h, time.Minute)
		lateAgain(t, h.herdr, "pane1")
		h.beads.add("B", "second", 0) // takes the one slot first
		h.worker("B", func(w *fakeWorker) AgentState {
			time.Sleep(20 * time.Minute)
			return finishes("b.txt")(w)
		})

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"  A's worker in tab tab1 came up without its name; named it A",
			"  A, which the last run left running (INTERRUPTED), is at work again in tab tab1, so its worker is adopted",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("run 2 events lack %q:\n%s", want, ev)
			}
		}
		if got := closedIDs(h); !equal(got, []string{"A", "B"}) || len(h.herdr.argsFor("A")) != 1 {
			t.Errorf("merged %v, A's worker started %d times; want A merged from its one worker, then B",
				got, len(h.herdr.argsFor("A")))
		}
	})
}

// Herdr numbers tabs and panes afresh when it restarts, so the pane is looked in only while Herdr
// has the worker's tab labelled with the ticket's ID: one labelled otherwise may be the user's, and
// the agent in the pane is left as it is. A label Herdr can't give leaves the worker carried over,
// unnamed, rather than dropped as gone.
func TestUnnamedWorkerIsNamedOnlyInItsTicketsTab(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		herdr func(h *fakeHerdr) // between the runs
		gone  bool               // A is dropped as gone (WORKER_GONE); otherwise left for the next run
	}{
		{"labelled otherwise", func(h *fakeHerdr) { h.labels["tab1"] = "notes" }, true},
		{"closed", func(h *fakeHerdr) { delete(h.labels, "tab1") }, true},
		{"Herdr can't say", func(h *fakeHerdr) { h.labelFails = true }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				<-cutUnnamed(t, h, 0)
				h.herdr.mu.Lock()
				c.herdr(h.herdr)
				h.herdr.mu.Unlock()

				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
					t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if name := paneName(h.herdr, "pane1"); name != "" {
					t.Errorf("the agent in pane1 was named %q; want it left unnamed", name)
				}
				gone := warnings(h, "A")
				if c.gone != (len(gone) == 1 && strings.Contains(gone[0], "WORKER_GONE: A is in progress")) {
					t.Errorf("A warned about (dropped as gone: %v):\n%s", c.gone, strings.Join(gone, "\n"))
				}
				if left := saved(t, h); c.gone == (len(left) == 1) {
					t.Errorf("run 2 saved %+v; want A left for the next run: %v", left, !c.gone)
				}
				if want := "cannot tell whether tab tab1 is still A's, to look for its worker there"; !c.gone &&
					!strings.Contains(h.logged(), want) {
					t.Errorf("log lacks %q:\n%s", want, h.logged())
				}
				if got := closedIDs(h); len(got) != 0 {
					t.Errorf("merged %v; want nothing", got)
				}
			})
		})
	}
}
