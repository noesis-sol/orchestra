package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A Claude worker that closes its ticket and stays idle after a tool-use record, with no Stop hook
// after it, settles by the idle grace; the settle line says who wrote that record, so the next such
// case can be traced to the session and agent that wrote it.
func TestIdleGraceSettleNamesTheRecordsWriter(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.commit("a.txt")
			w.close()
			hooks.report(w.wt, "Stop")
			hooks.mu.Lock()
			hooks.last[w.wt] = ToolUse{Event: "PreToolUse", Tool: "Write", At: time.Now(), Session: "s-other",
				Transcript: "/t/s-other.jsonl", Agent: "a9", AgentType: "memory"}
			hooks.mu.Unlock()
			return "idle"
		})
		start := time.Now()
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if took := time.Since(start); took < idleGrace {
			t.Errorf("settled after %s, before the idle grace", took)
		}
		want := "A settled: idle for 10m after a PreToolUse hook, with no Stop hook; that record was written at " +
			start.Format("15:04:05") + " by tool Write, session s-other, agent a9 (memory), transcript /t/s-other.jsonl"
		if logged := h.logged(); !strings.Contains(logged, want) {
			t.Errorf("the log should say who wrote the record, %q:\n%s", want, logged)
		}
	})
}

func TestToolUseReporter(t *testing.T) {
	at := time.Date(2026, 10, 7, 9, 24, 2, 0, time.Local)
	for _, c := range []struct {
		u    ToolUse
		want string
	}{
		{ToolUse{Event: "PreToolUse", Tool: "Bash", At: at, Session: "s", Transcript: "/t/s.jsonl"},
			"written at 09:24:02 by tool Bash, session s, main agent, transcript /t/s.jsonl"},
		{ToolUse{Event: "PreToolUse", AgentType: "Explore"},
			"written at an unknown time by tool unnamed, session unnamed, agent with no ID (Explore), transcript unnamed"},
		{ToolUse{Event: "PreToolUse", Tool: "Read", Agent: "a1"},
			"written at an unknown time by tool Read, session unnamed, agent a1 (no type), transcript unnamed"},
	} {
		if got := c.u.Reporter(); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.u, got, c.want)
		}
	}
}
