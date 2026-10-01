package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// stopsMidTicket is a worker turn that ends, at its Stop hook, with the ticket still in progress.
func stopsMidTicket(report func(wt, event string)) behaviour {
	return func(w *fakeWorker) AgentState {
		report(w.wt, "PreToolUse")
		w.claim()
		report(w.wt, "Stop")
		return "idle"
	}
}

// A Claude worker whose turn ends with its ticket still in progress is told to continue, and the
// ticket it then closes is merged.
func TestWorkerStoppedMidTicketIsToldToContinue(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", stopsMidTicket(hooks.report), func(w *fakeWorker) AgentState {
			hooks.report(w.wt, "PreToolUse")
			w.commit("a.txt")
			w.close()
			hooks.report(w.wt, "Stop")
			return "idle"
		})
		start := time.Now()
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if took := time.Since(start); took >= idleGrace {
			t.Errorf("merged after %s: the message to continue, not the idle grace, should get it going again", took)
		}
		if got := h.herdr.pastedTo(); strings.Join(got, ",") != "A" {
			t.Errorf("messages pasted to %v, want one to A", got)
		}
		if !strings.Contains(h.mainLog(), "A: add a.txt") {
			t.Errorf("main:\n%s", h.mainLog())
		}
		if logged := h.logged(); !strings.Contains(logged,
			"A's worker ended its turn with the ticket still in progress; told it to continue (1 of 2)") {
			t.Errorf("the log should say it was told to continue:\n%s", logged)
		}
	})
}

// A worker that keeps ending its turns with its ticket in progress is told to continue twice, no
// more; then the idle grace runs as before, and the run pauses for it.
func TestWorkerIsToldToContinueAtMostTwice(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		// A third message would find no behaviour left, which fails the test.
		h.worker("A", stopsMidTicket(hooks.report), stopsMidTicket(hooks.report), stopsMidTicket(hooks.report))
		o, code := h.run()
		if code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: A still in_progress") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.herdr.pastedTo(); strings.Join(got, ",") != "A,A" {
			t.Errorf("messages pasted to %v, want two to A", got)
		}
		logged := h.logged()
		for _, want := range []string{"told it to continue (1 of 2)", "told it to continue (2 of 2)",
			"A settled: idle for 10m with the ticket still in progress"} {
			if !strings.Contains(logged, want) {
				t.Errorf("the log lacks %q:\n%s", want, logged)
			}
		}
	})
}

// A worker waiting on a question it asked, even with its ticket left in progress, and a worker
// without hooks, which can't tell the end of its turn, are not told to continue.
func TestOnlyAWorkerOwingWorkIsToldToContinue(t *testing.T) {
	t.Parallel()
	t.Run("asked", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			hooks := &hookReporter{}
			h.reporter = hooks
			h.beads.add("A", "first", 1)
			h.worker("A", func(w *fakeWorker) AgentState {
				w.claim()
				w.ask("Q", "which way?") // and forgets to reopen A
				hooks.report(w.wt, "Stop")
				return "idle"
			})
			o, code := h.run()
			if code != ExitOK {
				t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			if got := h.herdr.pastedTo(); len(got) != 0 {
				t.Errorf("messages pasted to %v", got)
			}
			if got := h.sink.of(EvAsked); len(got) != 1 {
				t.Errorf("asked: %q", got)
			}
		})
	})
	t.Run("without hooks", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.beads.add("A", "first", 1)
			h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
			o, code := h.run()
			if code != ExitStuck {
				t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			if got := h.herdr.pastedTo(); len(got) != 0 {
				t.Errorf("messages pasted to %v", got)
			}
		})
	})
}
