package dispatch

import (
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/organ"
)

// fakeReviewer is a claude that keeps the evidence it is given in the file it returns and writes
// a one-line report.
func fakeReviewer(t *testing.T) (bin, evidence string) {
	t.Helper()
	dir := t.TempDir()
	evidence = filepath.Join(dir, "evidence")
	script := "#!/bin/sh\ncat > '" + evidence + "'\necho '{\"result\":\"Stopped after the running tickets, as asked.\"}'\n"
	return faketool.Write(t, dir, "claude", script), evidence
}

// Asked to stop after the running tickets, the run starts nothing more, lets both running tickets
// finish and merge, and ends with DRAINED and exit 0; the report is written as after any end.
func TestDrainFinishesTheRunningTicketsAndStartsNoMore(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.add("C", "third", 3) // no behaviours: a worker on C or D fails the test
		h.beads.add("D", "fourth", 4)
		drainLine := "DRAIN: stopping after the 2 running tickets finish (A, B): no new tickets will start, asked from the dashboard"
		var o *Loop
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			time.Sleep(time.Second) // B starts meanwhile
			o.Drain("from the dashboard")
			time.Sleep(3 * readyPoll) // a few ready checks
			return finishes("a.txt")(w)
		})
		h.worker("B", func(w *fakeWorker) AgentState {
			w.claim()
			time.Sleep(2 * time.Second) // A asks for the drain meanwhile
			return finishes("b.txt")(w)
		})
		o = h.loop()
		bin, evidence := fakeReviewer(t)
		o.organ = organ.Client{Bin: bin}
		o.StartTriage()

		code := o.Run(t.Context())
		if code != ExitOK || o.Final() != "DRAINED after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.dispatched(); !equal(got, []string{"A", "B"}) {
			t.Errorf("dispatched %v, want A and B only", got)
		}
		if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || !strings.Contains(log, "B: add b.txt") {
			t.Errorf("main:\n%s", log)
		}
		for _, id := range []string{"C", "D"} {
			if st := h.statusOf(id); st != "open" {
				t.Errorf("%s is %s, want open", id, st)
			}
		}
		if !strings.Contains(h.logged(), drainLine) {
			t.Errorf("log lacks %q:\n%s", drainLine, h.logged())
		}
		if h.alerts.has(drainLine) || !h.alerts.has("Finished the run · 2 tickets closed") {
			t.Errorf("notifications: %v, want the run finished and not the request", h.alerts.list())
		}

		// The organ phase: triage finishes, and the reviewer is told the maintainer asked for the end.
		o.FinishTriage(t.Context())
		report, err := o.Review(t.Context(), code, o.Final())
		if err != nil || !strings.Contains(report, "Stopped after the running tickets") {
			t.Fatalf("report %q, err %v", report, err)
		}
		given := read(t, evidence)
		for _, want := range []string{"the maintainer asked the run to stop after its running tickets",
			"## The run's final line\n\n<evidence id=\"", "\">\nDRAINED after 2 tickets\n</evidence id=\"", drainLine} {
			if !strings.Contains(given, want) {
				t.Errorf("the reviewer's evidence lacks %q:\n%s", want, given)
			}
		}
	})
}

// Taking the drain back lets the run take tickets again, including ones that became ready while
// it was winding down.
func TestResumeAfterDrainDispatchesAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "first", 1)
		var o *Loop
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			o.Drain("from the dashboard")
			w.beads.add("B", "follow-up", 2) // ready, with a slot free
			time.Sleep(3 * readyPoll)        // a few ready checks
			drainLine := "DRAIN: stopping after A finishes: no new tickets will start, asked from the dashboard"
			if got := h.sink.dispatched(); !equal(got, []string{"A"}) || !strings.Contains(h.sink.text(), drainLine) {
				t.Errorf("dispatched %v while winding down:\n%s", got, h.sink.text())
			}
			o.Resume("from the dashboard")
			time.Sleep(time.Second)
			if got := h.sink.dispatched(); !equal(got, []string{"A", "B"}) {
				t.Errorf("dispatched %v after the resume, want B too, beside A", got)
			}
			return finishes("a.txt")(w)
		})
		h.worker("B", finishes("b.txt"))
		o = h.loop()
		if code := o.Run(t.Context()); code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if want := "DRAIN cancelled: taking new tickets again, asked from the dashboard"; !strings.Contains(h.logged(), want) {
			t.Errorf("log lacks %q:\n%s", want, h.logged())
		}
		if log := h.mainLog(); !strings.Contains(log, "B: add b.txt") {
			t.Errorf("main:\n%s", log)
		}
	})
}

// With nothing running, a drain ends the run at once.
func TestDrainWithNothingRunningEndsTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1) // no behaviour: a worker on it fails the test
		o := h.loop()
		o.Drain("by SIGUSR1")
		start := time.Now()
		if code := o.Run(t.Context()); code != ExitOK || o.Final() != "DRAINED after 0 tickets" || time.Since(start) != 0 {
			t.Fatalf("exit %d, final %q after %s\n%s", code, o.Final(), time.Since(start), h.sink.text())
		}
		if want := "DRAIN: stopping now, as nothing is running, asked by SIGUSR1"; !strings.Contains(h.logged(), want) {
			t.Errorf("log lacks %q:\n%s", want, h.logged())
		}
	})
}
