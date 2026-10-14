package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A worker whose background subagent stops after the worker's turn ended, its ticket still in
// progress, is not told to continue in the moments before Herdr shows it working on the subagent's
// answer: the subagent's tool uses, reported after the Stop as forked agents' are, don't end a turn
// that its answer goes on with.
func TestWorkerIdleAsItsSubagentStopsIsNotNudged(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &recordReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			hooks.report(w.wt, "PreToolUse")
			w.claim()
			hooks.note(w.wt, func(r *HookRecord) { r.Subagents = 1 })
			stopped := time.Now()
			hooks.use(w.wt, ToolUse{Event: "Stop", Stopped: stopped})
			w.shows(StateIdle)
			for range 3 { // the subagent's tool uses, reported as the worker's
				time.Sleep(statusPoll)
				hooks.use(w.wt, ToolUse{Event: "PreToolUse", Tool: "Read", Stopped: stopped})
			}
			hooks.note(w.wt, func(r *HookRecord) { r.Subagents, r.SubagentStopped = 0, time.Now() })
			time.Sleep(5 * statusPoll) // its answer reaches the worker, still idle
			w.shows(StateWorking)
			w.commit("a.txt")
			w.close()
			hooks.report(w.wt, "Stop")
			return StateIdle
		})
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.herdr.pastedTo(); len(got) != 0 {
			t.Errorf("messages pasted to %v, want none\n%s", got, h.sink.text())
		}
		if !strings.Contains(h.mainLog(), "A: add a.txt") {
			t.Errorf("main:\n%s", h.mainLog())
		}
	})
}

// A subagent that stopped after the worker's Stop keeps the turn going, whether the Stop is the
// last report or a forked agent's PreToolUse replaced it, and the worker settles only after the idle
// grace, saying why; one that stopped before the Stop, or the stop the hooks don't place (a fork's,
// at the end of the turn), leaves the turn ended at the Stop.
func TestSubagentStoppedAfterTheStopIsMidTurn(t *testing.T) {
	stop := time.Now().Add(-time.Minute)
	hooks := &recordReporter{records: map[string]HookRecord{"wt": {SubagentStopped: stop.Add(time.Second)}}}
	w := worker{wt: "wt", hooks: true}
	o := &Loop{reporter: hooks}
	for _, u := range []ToolUse{
		{Event: "Stop", At: stop, Stopped: stop},
		{Event: "PreToolUse", Tool: "Read", At: stop.Add(time.Second), Stopped: stop},
	} {
		hooks.last = map[string]ToolUse{"wt": u}
		if _, ended, _ := turnEnded(o.hooksOf(w), time.Now()); ended {
			t.Errorf("%s: turn ended with a subagent stopped after the Stop", u.Event)
		}
		settled, why := idleSettled("closed", o.hooksOf(w), time.Time{}, time.Now(), time.Minute, time.Minute)
		if settled {
			t.Errorf("%s: settled (%s) a minute after a subagent stopped past the Stop", u.Event, why)
		}
		settled, why = idleSettled("closed", o.hooksOf(w), time.Time{}, time.Now(), idleGrace, time.Minute)
		if !settled || !strings.Contains(why, "after a subagent stopped") {
			t.Errorf("%s: after the idle grace: settled = %v (%s), want true, naming the subagent", u.Event, settled, why)
		}
	}
	u := ToolUse{Event: "Stop", At: stop, Stopped: stop}
	hooks.last = map[string]ToolUse{"wt": u}
	for _, rec := range []HookRecord{{SubagentStopped: stop.Add(-time.Second)}, {Noted: stop.Add(time.Second)}} {
		hooks.records["wt"] = rec
		if at, ended, _ := turnEnded(o.hooksOf(w), time.Now()); !ended || !at.Equal(stop) {
			t.Errorf("hooks' record %+v: turnEnded = %v, %v, want the Stop's time", rec, at, ended)
		}
	}
}
