package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A worker whose subagent stops long after the worker's turn ended is not paused in the moments
// before Herdr shows it working on the subagent's answer: its idle time counts from the subagent's
// stop, not from the Stop of the turn before.
func TestWorkerIdleAsItsSubagentStopsIsNotPaused(t *testing.T) {
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
			hooks.report(w.wt, "Stop")
			w.shows(StateIdle)
			for range 3 { // the subagent's tool uses, reported as the worker's
				time.Sleep(idleGrace / 2)
				hooks.report(w.wt, "PreToolUse")
			}
			time.Sleep(idleGrace / 2) // the subagent writes its answer
			hooks.note(w.wt, func(r *HookRecord) { r.Subagents = 0 })
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
			t.Errorf("messages pasted to %v, want none", got)
		}
		if !strings.Contains(h.mainLog(), "A: add a.txt") {
			t.Errorf("main:\n%s", h.mainLog())
		}
	})
}

// A worker idle past the grace, its last subagent stopped a moment ago, has been quiet only since
// that stop; one whose hooks noted nothing since it went idle, since then.
func TestQuietForCountsFromTheHooksLastNote(t *testing.T) {
	now := time.Now()
	since := now.Add(-2 * idleGrace)
	w := worker{wt: "wt", hooks: true}
	hooks := &recordReporter{records: map[string]HookRecord{"wt": {Noted: now.Add(-time.Minute)}}}
	o := New(Config{}, nil, "", Deps{Reporter: hooks})
	if got := quietFor(o.hooksOf(w), since, time.Now()); got > 2*time.Minute {
		t.Errorf("quiet for %s a minute after a subagent stopped, want about a minute", got)
	}
	hooks.records["wt"] = HookRecord{Noted: now.Add(-3 * idleGrace)}
	if got := quietFor(o.hooksOf(w), since, time.Now()); got < 2*idleGrace {
		t.Errorf("quiet for %s, noted before it went idle %s ago", got, 2*idleGrace)
	}
	w.hooks = false
	hooks.records["wt"] = HookRecord{Noted: now}
	if got := quietFor(o.hooksOf(w), since, time.Now()); got < 2*idleGrace {
		t.Errorf("quiet for %s without hooks, idle %s", got, 2*idleGrace)
	}
}
