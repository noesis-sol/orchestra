package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// lateNotes holds triage's notes back until the probe has ended the hold. Triage takes a model call
// per ticket, so with many tickets deferred at once its verdicts on them can come after the probe;
// it writes each note just before handing its verdict over, so the verdict waits too.
type lateNotes struct {
	Notes
	sink *runSink
}

func (n lateNotes) AppendNotes(ctx context.Context, id, note string) error {
	if strings.HasPrefix(note, "Triage") {
		waitFor(func() bool { return len(n.sink.of(EvProbed)) > 0 })
	}
	return n.Notes.AppendNotes(ctx, id, note)
}

// waitFor waits on the bubble's clock until cond holds, or for an hour of it: a test that never
// sees it fails on the run's outcome.
func waitFor(cond func() bool) {
	for deadline := time.Now().Add(time.Hour); !cond() && time.Now().Before(deadline); {
		time.Sleep(time.Second)
	}
}

// triagedLate runs h's loop with triage blaming the environment for every deferral, its verdicts
// held back until a probe has ended the hold.
func (h *harness) triagedLate(t *testing.T) (*Loop, int) {
	o := h.loop()
	o.organ = organ.Client{Bin: fakeTriage(t)}
	o.notes = lateNotes{o.notes, h.sink}
	o.StartTriage()
	code := o.Run(t.Context())
	o.FinishTriage(t.Context())
	return o, code
}

// Workers failing at once hold the run, and the probe finds the machine working. Triage's verdicts
// on those tickets, blaming the environment, come after it, while the tickets run again: they hold
// nothing.
func TestVerdictsFromBeforeTheProbeDontHoldTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		h.cfg.EnvProbe = time.Minute
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.beads.add("C", "c", 3)
		h.worker("A", givesUp, finishes("a.txt"))
		h.worker("B", givesUp, finishes("b.txt"))
		// C keeps the run going until triage has given both verdicts.
		h.worker("C", func(w *fakeWorker) AgentState {
			waitFor(func() bool { return len(h.sink.of(EvTriage)) == 2 })
			return finishes("c.txt")(w)
		})
		h.worker(probeID, runsProbe)

		o, code := h.triagedLate(t)
		if code != ExitOK {
			t.Errorf("exit code %d, want %d:\n%s", code, ExitOK, h.logged())
		}
		if got := h.dispatched(); !equal(got, []string{"A", "B", "A", "B", "C"}) {
			t.Errorf("dispatched %v, want A and B, then after the probe A, B and C", got)
		}
		if got := h.sink.of(EvTriage); len(got) != 2 {
			t.Errorf("triage verdicts %q, want A's and B's", got)
		}
		for _, id := range []string{"A", "B", "C"} {
			if st := h.statusOf(id); st != "closed" {
				t.Errorf("%s is %s, want closed", id, st)
			}
		}
		if !strings.HasPrefix(o.Final(), "READY_EMPTY") {
			t.Errorf("final line %q", o.Final())
		}
	})
}

// Verdicts on tickets deferred after the probe still hold the run, which ends: the machine is
// probed once.
func TestVerdictsFromAfterTheProbeHoldTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		h.cfg.EnvProbe = time.Minute
		defers := func(w *fakeWorker) AgentState { w.claim(); w.deferIt(); return "idle" }
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.beads.add("C", "c", 3)
		h.beads.add("D", "d", 4)
		h.beads.add("E", "e", 5)
		h.worker("A", givesUp, finishes("a.txt"))
		h.worker("B", givesUp, finishes("b.txt"))
		h.worker("C", defers)
		h.worker("D", defers)
		// E, if it starts before D's verdict, finishes once the run holds.
		h.worker("E", func(w *fakeWorker) AgentState {
			waitFor(func() bool { return strings.Contains(h.sink.text(), "HOLD: ENVIRONMENT: triage blamed") })
			return finishes("e.txt")(w)
		})
		h.worker(probeID, runsProbe)

		o, code := h.triagedLate(t)
		if code != ExitEnvironment {
			t.Errorf("exit code %d, want %d:\n%s", code, ExitEnvironment, h.logged())
		}
		final := "ENVIRONMENT: triage blamed the environment for the last 2 tickets (C, D) with high confidence " +
			"(Classifier unavailable); check the machine, then restart"
		if o.Final() != final {
			t.Errorf("final line %q, want %q", o.Final(), final)
		}
		for id, want := range map[string]TicketStatus{"A": "closed", "B": "closed", "C": "deferred", "D": "deferred"} {
			if st := h.statusOf(id); st != want {
				t.Errorf("%s is %s, want %s", id, st, want)
			}
		}
	})
}
