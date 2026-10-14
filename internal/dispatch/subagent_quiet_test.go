package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A subagent cut off by Esc runs no SubagentStop, so the hooks' record counts it running for good.
// Once no hook has reported anything for SubagentQuiet it counts as ended: the worker's Stop, its
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
		if took := time.Since(stopped); took < SubagentQuiet {
			t.Errorf("merged %s after the Stop: the subagent counts as running until no hook for %s",
				took, SubagentQuiet)
		}
	})
}

// A subagent counts as running while hooks report, by a note or a tool use of any agent, and no
// longer once none has for SubagentQuiet; with none started, none runs.
func TestSubagentsRunning(t *testing.T) {
	now := time.Now()
	recent, old := now.Add(-time.Minute), now.Add(-SubagentQuiet)
	for _, c := range []struct {
		name        string
		rec         HookRecord
		used        time.Time
		wantRunning bool
	}{
		{"noted lately", HookRecord{Subagents: 1, Noted: recent}, old, true},
		{"a tool use lately", HookRecord{Subagents: 1, Noted: old}, recent, true},
		{"quiet", HookRecord{Subagents: 1, Noted: old}, old, false},
		{"nothing known", HookRecord{Subagents: 1}, time.Time{}, false},
		{"none started", HookRecord{Noted: now}, now, false},
	} {
		if got := c.rec.SubagentsRunning(c.used, now); got != c.wantRunning {
			t.Errorf("%s: running = %v, want %v", c.name, got, c.wantRunning)
		}
	}
}
