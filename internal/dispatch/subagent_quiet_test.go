package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// A subagent cut off by Esc runs no SubagentStop, so the hooks' record counts it running for good.
// Once no hook has reported anything for agentio.SubagentQuiet it counts as ended: the worker's Stop, its
// ticket still in progress, ends its turn, and the worker is told to continue rather than left to
// the idle grace.
func TestSubagentQuietSoLongNoLongerHoldsTheTurn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &recordReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		var stopped time.Time
		h.worker("A", func(w *fakeWorker) AgentState {
			hooks.report(w.wt, "PreToolUse")
			w.claim()
			hooks.note(w.wt, func(r *HookRecord) { r.Subagents, r.Noted = 1, time.Now() })
			hooks.report(w.wt, "PreToolUse") // the subagent's, then Esc: no SubagentStop
			time.Sleep(time.Minute)
			hooks.report(w.wt, "Stop")
			stopped = time.Now()
			return StateIdle
		}, func(w *fakeWorker) AgentState {
			hooks.report(w.wt, "PreToolUse")
			w.commit("a.txt")
			w.close()
			hooks.report(w.wt, "Stop")
			return StateIdle
		})
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.herdr.pastedTo(); strings.Join(got, ",") != "A" {
			t.Errorf("messages pasted to %v, want one to A", got)
		}
		if !strings.Contains(h.mainLog(), "A: add a.txt") {
			t.Errorf("main:\n%s", h.mainLog())
		}
		if took := time.Since(stopped); took < agentio.SubagentQuiet {
			t.Errorf("merged %s after the Stop: the subagent counts as running until no hook for %s",
				took, agentio.SubagentQuiet)
		}
	})
}

// A subagent's quiet, agentio's, is the loop's idle grace: one left that long settles its worker too.
func TestSubagentQuietIsTheIdleGrace(t *testing.T) {
	t.Parallel()
	if agentio.SubagentQuiet != idleGrace {
		t.Errorf("agentio.SubagentQuiet is %s, the idle grace %s", agentio.SubagentQuiet, idleGrace)
	}
}
