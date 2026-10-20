package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// waitsForApproval is a worker that, answered in its tab, carries on and asks to run a Bash command,
// which Herdr shows as idle; claim says whether it claimed its ticket again first. After wait the
// maintainer allows it: it claims the ticket, commits file and closes it. Told anything while it
// waits, it fails the test: the message's Enter would have allowed the command.
func waitsForApproval(hooks *recordReporter, claim bool, wait time.Duration, file string) behaviour {
	return func(w *fakeWorker) AgentState {
		if claim {
			w.claim()
		}
		hooks.use(w.wt, ToolUse{Event: agentio.EventPermission, Tool: "Bash", Command: "rm -rf build/*"})
		w.shows(StateIdle)
		time.Sleep(wait)
		w.shows(StateWorking)
		hooks.use(w.wt, ToolUse{Event: "PreToolUse", Tool: "Bash", Command: "rm -rf build/*"})
		w.claim()
		w.commit(file)
		w.close()
		hooks.report(w.wt, "Stop")
		return StateIdle
	}
}

// An asked ticket claimed again whose worker, answered in its tab, waits on a permission prompt Herdr
// shows as idle is adopted without being told the answer is in: that would allow the command. It is
// merged once the maintainer allows it and it finishes.
func TestAskedTicketClaimedAgainAtAPermissionPromptIsNotTold(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &recordReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		answered := make(chan struct{})
		h.worker("A", asksThenCarriesOn(&hooks.hookReporter, answered, func(w *fakeWorker) AgentState {
			time.Sleep(time.Minute)
			return waitsForApproval(hooks, true, 2*time.Minute, "a.txt")(w)
		}))
		h.worker("B", answersAfter(time.Minute+7*time.Second, answered, time.Hour, "b.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.herdr.pastedTo(); len(got) != 0 {
			t.Errorf("pasted to %v; a worker at a permission prompt must not be told anything", got)
		}
		if ev := h.sink.text(); !strings.Contains(ev,
			"A's worker in tab tab1 waits on a permission prompt with the ticket in_progress; adopting it rather "+
				"than telling it Q is answered") {
			t.Errorf("A's worker should have been adopted at its prompt:\n%s", ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A", "B"}) {
			t.Errorf("merged %v", got)
		}
	})
}

// An asked ticket back through bd ready whose earlier worker, answered in its tab, waits on a
// permission prompt Herdr shows as idle is adopted without being told the answer is in, and merged
// once the maintainer allows the command and it finishes.
func TestAskedTicketBackWithItsWorkerAtAPermissionPromptIsNotTold(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &recordReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", func(w *fakeWorker) AgentState {
			hooks.report(w.wt, "PreToolUse")
			w.claim()
			w.ask("Q", "which way?")
			hooks.report(w.wt, "Stop")
			w.shows(StateIdle)
			time.Sleep(time.Minute) // answered in its tab, it carries on without claiming A again
			return waitsForApproval(hooks, false, 3*time.Minute, "a.txt")(w)
		})
		h.worker("B", func(w *fakeWorker) AgentState {
			time.Sleep(2 * time.Minute)
			w.beads.set("Q", "closed")
			return finishes("b.txt")(w)
		})
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.herdr.pastedTo(); len(got) != 0 {
			t.Errorf("pasted to %v; a worker at a permission prompt must not be told anything", got)
		}
		if ev := h.sink.text(); !strings.Contains(ev,
			"A's earlier worker in tab tab1 waits on a permission prompt; adopting it rather than telling it Q is answered") {
			t.Errorf("A's earlier worker should have been adopted at its prompt:\n%s", ev)
		}
		if got := closedIDs(h); !equal(got, []string{"B", "A"}) {
			t.Errorf("merged %v", got)
		}
	})
}
