package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A worker that ends its turn to wait on a subagent in the background (the verifier, say), its
// ticket still in progress, is neither told to continue nor settled while the subagent works, even
// past the idle grace while the subagent's tool uses go on; the subagent's answer starts its next
// turn, which closes the ticket.
func TestWorkerWaitsOnASubagentPastItsStop(t *testing.T) {
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
			for range 4 { // the subagent's tool uses, reported as the worker's
				time.Sleep(idleGrace / 2)
				hooks.report(w.wt, "PreToolUse")
			}
			hooks.note(w.wt, func(r *HookRecord) { r.Subagents = 0 })
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
			t.Errorf("messages pasted to %v, want none while its subagent runs", got)
		}
		if !strings.Contains(h.mainLog(), "A: add a.txt") {
			t.Errorf("main:\n%s", h.mainLog())
		}
	})
}

// A Stop with a subagent still running doesn't end the turn: the worker settles only after the idle
// grace, and says why.
func TestStopWithASubagentRunningIsMidTurn(t *testing.T) {
	stop := time.Now()
	u := ToolUse{Event: "Stop", At: stop, Stopped: stop}
	o := &Loop{reporter: &subagentsRunning{hookReporter{last: map[string]ToolUse{"wt": u}}}}
	if _, ended, _ := o.turnEnded("wt", u); ended {
		t.Error("turn ended at a Stop with a subagent running")
	}
	settled, why := o.idleSettled("closed", "wt", true, time.Time{}, time.Minute, time.Minute)
	if settled {
		t.Errorf("settled (%s) a minute after a Stop with a subagent running", why)
	}
	settled, why = o.idleSettled("closed", "wt", true, time.Time{}, idleGrace, time.Minute)
	if !settled || !strings.Contains(why, "with a subagent still running") {
		t.Errorf("after the idle grace: settled = %v (%s), want true, naming the subagent", settled, why)
	}
	o.reporter = &hookReporter{last: map[string]ToolUse{"wt": u}}
	if at, ended, _ := o.turnEnded("wt", u); !ended || !at.Equal(stop) {
		t.Errorf("with no subagent: turnEnded = %v, %v, want the Stop's time", at, ended)
	}
}
