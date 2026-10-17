package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// Claude Code runs a turn's Stop hooks before it logs the turn's end and the notice that the turn ended at
// the usage limit: for a moment, longer while the user's own Stop hooks are slow, the hooks report a Stop
// that is still ending (ToolUse.Ending), which isn't yet the turn's end.

// endingStop reports, as the hooks of the worker in wt do, a turn that ended just now with a Stop whose end
// its transcript has yet to log.
func (r *hookReporter) endingStop(wt string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		r.last = map[string]ToolUse{}
	}
	now := time.Now()
	r.last[wt] = ToolUse{Event: "Stop", At: now, Stopped: now, Ending: true}
}

// A worker that defers its ticket as it stops after Claude Code's grace note, its hooks reporting the Stop
// first, Herdr showing it idle, and the limit only a few polls later, isn't set aside at the Stop: it waits
// in the run's hold, its ticket brought back in progress, and carries on after the reset.
func TestAStopStillEndingIsNotTheTurnsEnd(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.UsageLimitWait = 6 * time.Hour
		reset := time.Now().Add(time.Hour)
		h.beads.add("A", "wraps up at the limit", 1)
		var told TicketStatus
		h.worker("A", func(w *fakeWorker) AgentState {
			hooks.report(w.wt, "PreToolUse")
			w.claim()
			w.commit("a.txt")
			w.deferIt()
			hooks.endingStop(w.wt)
			w.shows(StateIdle)
			time.Sleep(3 * statusPoll) // the user's own Stop hooks run
			hooks.graceStop(w.wt, reset)
			return StateLimited
		}, statusWhenTold(&told, carriesOn(hooks, "a2.txt", 2*time.Minute)))
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		for _, f := range []string{"A: add a.txt", "A: add a2.txt"} {
			if !strings.Contains(h.mainLog(), f) {
				t.Errorf("main lacks %q:\n%s", f, h.mainLog())
			}
		}
		if told != StatusInProgress {
			t.Errorf("A was %s as its worker was told to carry on, want in progress", told)
		}
		if held := h.sink.eventsOf(EvLimited); len(held) != 1 || held[0].Ticket != "A" {
			t.Errorf("usage limit events: %+v", held)
		}
		logged := h.logged()
		if settled := strings.Count(logged, "A settled: "); settled != 1 {
			t.Errorf("A settled %d times, want once, as it closed after the reset:\n%s", settled, logged)
		}
		for _, not := range []string{"deferred by worker", "set aside"} {
			if strings.Contains(logged, not) {
				t.Errorf("the log has %q:\n%s", not, logged)
			}
		}
	})
}
