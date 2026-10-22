package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// idleLong is longer than any of the waits in these tests should last.
const idleLong = 10 * time.Hour

// whenWaiting runs do once the run says it waits for the filing.
func whenWaiting(h *harness, do func()) {
	go func() {
		waitFor(func() bool { return strings.Contains(h.sink.text(), "waiting for the rest of R to be filed") })
		do()
	}()
}

// Asked to wind down while it waits for the rest of the scope to be filed, with nothing running, the run
// ends at once, as its DRAIN line says, rather than wait for the breakdown, which never ends here.
func TestADrainEndsTheWaitForFiling(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, _ := filingHarness(t)
		h.worker("R.1", finishes("setup.txt"))
		o := h.loop()
		whenWaiting(h, func() { o.Drain("from the dashboard") })
		code := o.Run(context.Background())
		if code != ExitOK || !strings.HasPrefix(o.Final(), "DRAINED after 1 tickets;") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.dispatched(); !equal(got, []string{"R.1"}) {
			t.Errorf("dispatched %v, want R.1 alone", got)
		}
	})
}

// The environment ticket's worker going idle with its ticket in progress stops the run (PAUSED): with
// nothing else running, it ends with that, rather than wait for the breakdown, which never ends here.
func TestAStopEndsTheWaitForFiling(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, _ := filingHarness(t)
		h.worker("R.1", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
		o, code := h.run()
		if code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: R.1 still in_progress") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if strings.Contains(h.sink.text(), "waiting for the rest of R to be filed") {
			t.Errorf("the run waited for the filing once stopped:\n%s", h.sink.text())
		}
	})
}

// blameEnvironment has triage hold o for the environment, as its verdict on ticket id does when it
// is the first of a row of one.
func blameEnvironment(o *Loop, id string) {
	o.blamed(context.Background(), verdict{id: id, cause: "environment", confidence: "high",
		summary: "the machine fails", gen: o.envGen.Load()})
}

// A triage verdict that holds the run for the environment ends whichever wait the run is in with
// nothing running, for the filing, the usage limit's reset or the maintainer's answers: with no probe,
// the run ends at once, held for the environment.
func TestAnEnvironmentVerdictEndsEveryIdleWait(t *testing.T) {
	t.Parallel()
	const want = "ENVIRONMENT: triage blamed the environment for the last 1 tickets (X) with high confidence " +
		"(the machine fails); check the machine, then restart"
	for _, tc := range []struct {
		name  string
		setUp func(t *testing.T) (*harness, func(o *Loop)) // the run, and what it does as the run starts
	}{
		{"filing", func(t *testing.T) (*harness, func(o *Loop)) {
			h, _ := filingHarness(t) // the breakdown never ends
			h.worker("R.1", finishes("setup.txt"))
			return h, func(o *Loop) { whenWaiting(h, func() { blameEnvironment(o, "X") }) }
		}},
		{"usage limit", func(t *testing.T) (*harness, func(o *Loop)) {
			h := newTimedHarness(t)
			hooks := &hookReporter{}
			h.reporter = hooks
			h.cfg.UsageLimitWait = idleLong
			reset := time.Now().Add(idleLong / 2)
			h.beads.add("A", "a", 1)
			h.beads.add("B", "b", 2) // no behaviour: a worker on it fails the test
			h.worker("A", func(w *fakeWorker) AgentState {
				hooks.limit(w.wt, reset)
				w.shows(StateLimited)
				time.Sleep(10 * time.Minute)
				return StateGone // its tab closed: nothing runs while the run holds for the limit
			})
			return h, func(o *Loop) {
				go func() {
					time.Sleep(time.Hour)
					blameEnvironment(o, "X")
				}()
			}
		}},
		{"answers", func(t *testing.T) (*harness, func(o *Loop)) {
			h := newTimedHarness(t)
			h.beads.add("A", "ticket A", 1) // no behaviour: a worker on it fails the test
			h.beads.add("Q", "which way?", 1, HumanLabel)
			h.beads.link("A", "Q", "blocks")
			answers := &fakeAnswers{beads: h.beads, closeAfter: idleLong}
			t.Cleanup(answers.done)
			return h, func(o *Loop) {
				o.SetAnswers(answers)
				go func() {
					waitFor(func() bool { return strings.Contains(h.sink.text(), "the answer talk is open") })
					blameEnvironment(o, "X")
				}()
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h, starts := tc.setUp(t)
				h.cfg.EnvHoldCount = 1 // triage hands Run its verdicts
				o := h.loop()
				starts(o)
				start := time.Now()
				code := o.Run(context.Background())
				if code != ExitEnvironment || !strings.HasPrefix(o.Final(), want) {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if took := time.Since(start); took >= idleLong/2 {
					t.Errorf("the run waited %v after the verdict", took)
				}
			})
		})
	}
}
