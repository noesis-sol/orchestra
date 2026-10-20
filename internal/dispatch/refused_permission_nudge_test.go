package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// refusesPermission is a worker turn that asks to run a Bash command, which the maintainer refuses
// after a while: no hook runs, so the PermissionRequest stays the hooks' last report, until Claude
// Code notes the worker idle at its prompt (ToolUse.Idle) a minute on. Its ticket stays in progress.
func refusesPermission(hooks *recordReporter) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		hooks.use(w.wt, ToolUse{Event: "PreToolUse", Tool: "Bash", Command: "rm -rf build"})
		hooks.use(w.wt, ToolUse{Event: agentio.EventPermission, Tool: "Bash", Command: "rm -rf build"})
		w.shows(StateBlocked)
		time.Sleep(30 * time.Second) // the maintainer presses Esc
		w.shows(StateIdle)
		time.Sleep(time.Minute) // Claude Code's idle_prompt notification
		hooks.mu.Lock()
		u := hooks.last[w.wt]
		u.Idle = time.Now()
		hooks.last[w.wt] = u
		hooks.mu.Unlock()
		return StateIdle
	}
}

// A worker idle after a refused permission prompt, its ticket still in progress, has ended its turn as
// one at its Stop hook has: it is told to continue, and the ticket it then closes is merged, without
// waiting out the idle grace.
func TestWorkerIdleAfterARefusedPromptIsToldToContinue(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &recordReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", refusesPermission(hooks), func(w *fakeWorker) AgentState {
			hooks.report(w.wt, "PreToolUse")
			w.commit("a.txt")
			w.close()
			hooks.report(w.wt, "Stop")
			return StateIdle
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
		if logged := h.logged(); !strings.Contains(logged,
			"A's worker ended its turn with the ticket still in progress; told it to continue (1 of 2)") {
			t.Errorf("the log should say it was told to continue:\n%s", logged)
		}
	})
}

// A worker idle after a refused permission prompt with its ticket waiting on a question it asked has
// settled there, as at a Stop hook: it is not told to continue.
func TestWorkerIdleAfterARefusedPromptWithAQuestionSettles(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &recordReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		refuses := refusesPermission(hooks)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.ask("Q", "may I remove build?") // and forgets to reopen A
			return refuses(w)
		})
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.herdr.pastedTo(); len(got) != 0 {
			t.Errorf("messages pasted to %v", got)
		}
		if logged := h.logged(); !strings.Contains(logged, "A settled: idle at its prompt at ") ||
			!strings.Contains(logged, " after a permission prompt for Bash refused, waiting on Q") {
			t.Errorf("the log should say it settled at the refusal, waiting on Q:\n%s", logged)
		}
	})
}
