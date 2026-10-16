package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A permission prompt the maintainer refused runs no hook, so the hooks' last report stays the
// PermissionRequest; once Claude Code notes the worker idle at its prompt after it (ToolUse.Idle), the
// worker waits on nothing: the dashboard stops showing a prompt, and the run doesn't stop for one
// after the blocked limit, but lets the idle worker, its ticket still in progress, settle after the idle
// grace, and the run pauses for it.
func TestRefusedPermissionPromptDoesNotStopAsBlocked(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &recordReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		var shown Status
		var refused time.Time
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			hooks.use(w.wt, ToolUse{Event: "PreToolUse", Tool: "Bash", Command: "rm -rf build"})
			hooks.use(w.wt, ToolUse{Event: EventPermission, Tool: "Bash", Command: "rm -rf build"})
			w.shows(StateBlocked)
			time.Sleep(30 * time.Second) // the maintainer presses Esc
			refused = time.Now()
			w.shows(StateIdle)
			time.Sleep(time.Minute) // Claude Code's idle_prompt notification
			hooks.mu.Lock()
			u := hooks.last[w.wt]
			u.Idle = time.Now()
			hooks.last[w.wt] = u
			hooks.mu.Unlock()
			time.Sleep(3 * statusPoll)
			shown = h.sink.lastStatus("A")
			return StateIdle
		})
		o, code := h.run()
		if !strings.HasPrefix(o.Final(), "PAUSED: A still in_progress") ||
			!strings.Contains(h.sink.text(), "A settled: idle for 10m with the ticket still in progress") {
			t.Fatalf("want A settled after the idle grace and the run paused for it: exit %d, final %q\n%s",
				code, o.Final(), h.sink.text())
		}
		if shown.Permission || shown.Agent != StateIdle {
			t.Errorf("the dashboard should show the worker idle, with no prompt: %+v", shown)
		}
		if took := time.Since(refused); took < idleGrace {
			t.Errorf("the run ended %s after the refusal, want past the idle grace %s", took, idleGrace)
		}
	})
}
