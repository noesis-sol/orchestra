package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// replaceStop has an agent Claude Code forks at the end of the turn of the worker in wt run its
// PreToolUse hook, after the worker's Stop: the record of the tool use replaces the Stop's, and the
// tool is refused, so no PostToolUse follows.
func (r *hookReporter) replaceStop(wt string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	stop := r.last[wt]
	r.last[wt] = ToolUse{Event: "PreToolUse", Tool: "Read", At: time.Now(), Stopped: stop.At, Session: "s1"}
}

// A worker whose Stop a PreToolUse record of no turn replaced settles at that Stop, as at any
// other, rather than after the idle grace, and the log says who wrote the record.
func TestWorkerSettlesAtAStopAToolUseReplaced(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			hooks.report(w.wt, "PreToolUse")
			w.claim()
			w.commit("a.txt")
			w.close()
			hooks.report(w.wt, "Stop")
			hooks.replaceStop(w.wt)
			return "idle"
		})
		start := time.Now()
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if took := time.Since(start); took >= idleGrace {
			t.Errorf("settled after %s, want at its Stop hook", took)
		}
		logged := h.logged()
		for _, want := range []string{"A settled: Stop hook at ",
			"; then a PreToolUse record with no turn of its own, written at "} {
			if !strings.Contains(logged, want) {
				t.Errorf("the log lacks %q:\n%s", want, logged)
			}
		}
	})
}

// A worker whose turn ends with its ticket in progress is told to continue even when a PreToolUse
// record of no turn replaced its Stop.
func TestWorkerIsToldToContinueAfterAStopAToolUseReplaced(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			stopsMidTicket(hooks.report)(w)
			hooks.replaceStop(w.wt)
			return "idle"
		}, func(w *fakeWorker) AgentState {
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
	})
}

// subagentsRunning is hooks whose record shows a subagent started and not stopped.
type subagentsRunning struct{ hookReporter }

func (*subagentsRunning) HookRecord(string) HookRecord { return HookRecord{Subagents: 1} }

// Only a PreToolUse record written after a Stop no prompt has cleared, while no subagent runs,
// leaves the turn ended; an adopted worker's Stop from before its adoption doesn't count.
func TestTurnEndedThroughAReplacedStop(t *testing.T) {
	since := time.Now()
	stop := since.Add(time.Second)
	cases := []struct {
		name      string
		u         ToolUse
		subagents bool
		since     time.Time
		settled   bool
	}{
		{"replaced Stop", ToolUse{Event: "PreToolUse", At: stop.Add(2 * time.Second), Stopped: stop}, false, since, true},
		{"written with the Stop", ToolUse{Event: "PreToolUse", At: stop, Stopped: stop}, false, since, true},
		{"no Stop", ToolUse{Event: "PreToolUse", At: stop}, false, since, false},
		{"tool use before the Stop", ToolUse{Event: "PreToolUse", At: stop.Add(-time.Second), Stopped: stop},
			false, time.Time{}, false},
		{"finished tool after the Stop", ToolUse{Event: "PostToolUse", At: stop.Add(time.Second), Stopped: stop},
			false, since, false},
		{"permission prompt after the Stop", ToolUse{Event: EventPermission, At: stop.Add(time.Second), Stopped: stop},
			false, since, false},
		{"subagent running", ToolUse{Event: "PreToolUse", At: stop.Add(time.Second), Stopped: stop}, true, since, false},
		{"Stop before the adoption", ToolUse{Event: "PreToolUse", At: since.Add(time.Second),
			Stopped: since.Add(-time.Hour)}, false, since, false},
		{"Stop before, not adopted", ToolUse{Event: "PreToolUse", At: since.Add(time.Second),
			Stopped: since.Add(-time.Hour)}, false, time.Time{}, true},
	}
	for _, c := range cases {
		var r Reporter = &hookReporter{last: map[string]ToolUse{"wt": c.u}}
		if c.subagents {
			r = &subagentsRunning{hookReporter{last: map[string]ToolUse{"wt": c.u}}}
		}
		o := &Loop{reporter: r}
		settled, why := o.idleSettled("closed", "wt", true, c.since, time.Minute, time.Minute)
		if c.settled && (!settled || !strings.HasPrefix(why, "Stop hook at ")) ||
			!c.settled && settled {
			t.Errorf("%s: settled = %v (%s), want %v", c.name, settled, why, c.settled)
		}
	}
}
