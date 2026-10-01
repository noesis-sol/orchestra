package dispatch

import (
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// hookReporter is the hooks of Claude workers: each worker reports what it does in its worktree.
type hookReporter struct {
	fakeReporter
	mu   sync.Mutex
	last map[string]ToolUse
}

func (r *hookReporter) report(wt, event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		r.last = map[string]ToolUse{}
	}
	r.last[wt] = ToolUse{Event: event, At: time.Now()}
}

func (r *hookReporter) LastToolUse(wt string) (ToolUse, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	u, ok := r.last[wt]
	return u, ok
}

// startsSlowly is a worker Herdr shows as idle from its first command on, its ticket still open,
// for slow, then goes on to claim it, commit file and close it.
func startsSlowly(file string, slow time.Duration, report func(wt, event string)) behaviour {
	return func(w *fakeWorker) AgentState {
		if report != nil {
			report(w.wt, "PreToolUse") // cat .orchestra/run/prompt.md
		}
		w.shows("idle")
		time.Sleep(slow)
		w.claim()
		w.commit(file)
		w.close()
		if report != nil {
			report(w.wt, "Stop")
		}
		return "idle"
	}
}

// A Claude worker Herdr shows as idle before its Stop hook, its ticket still open, is mid-turn: it
// isn't deferred, though it stays idle past the start-up grace, and its close is merged.
func TestIdleWorkerBeforeItsStopHookIsNotDeferred(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", startsSlowly("a.txt", startGrace+time.Minute, hooks.report))
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.of(EvDeferred); len(got) != 0 {
			t.Errorf("deferred: %q", got)
		}
		if st := h.statusOf("A"); st != "closed" || !strings.Contains(h.mainLog(), "A: add a.txt") {
			t.Errorf("A is %s; main:\n%s", st, h.mainLog())
		}
		if logged := h.logged(); !strings.Contains(logged, "A settled: Stop hook at ") {
			t.Errorf("the log should say the Stop hook settled it:\n%s", logged)
		}
	})
}

// A Claude worker whose Stop hook came with its ticket still open has finished without closing it:
// deferred for review as before, without waiting out a start-up grace.
func TestWorkerStoppedWithItsTicketOpenIsDeferred(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState { hooks.report(w.wt, "Stop"); return "idle" })
		start := time.Now()
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if took := time.Since(start); took >= startGrace {
			t.Errorf("settled after %s, the start-up grace", took)
		}
		if got := h.sink.of(EvDeferred); len(got) != 1 || !strings.Contains(got[0], "A still open -> noted and deferred") {
			t.Errorf("deferred: %q", got)
		}
		if logged := h.logged(); !strings.Contains(logged, "A settled: Stop hook at ") {
			t.Errorf("the log should say the Stop hook settled it:\n%s", logged)
		}
	})
}

// Without hooks, an idle worker gets a start-up grace to claim its ticket: one that does within it
// is merged, and one that doesn't is deferred once it is over.
func TestWithoutHooksAnOpenTicketGetsAStartUpGrace(t *testing.T) {
	t.Parallel()
	t.Run("claimed within it", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.beads.add("A", "first", 1)
			h.worker("A", startsSlowly("a.txt", startGrace-time.Minute, nil))
			o, code := h.run()
			if code != ExitOK {
				t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			if got := h.sink.of(EvDeferred); len(got) != 0 {
				t.Errorf("deferred: %q", got)
			}
			if !strings.Contains(h.mainLog(), "A: add a.txt") {
				t.Errorf("main:\n%s", h.mainLog())
			}
		})
	})
	t.Run("left open", func(t *testing.T) {
		t.Parallel()
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.beads.add("A", "first", 1)
			h.worker("A", func(w *fakeWorker) AgentState { return "idle" })
			began := time.Now()
			o, code := h.run()
			if code != ExitOK {
				t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			if took := time.Since(began); took < startGrace || took > startGrace+statusPoll {
				t.Errorf("settled after %s, want once the %s grace is over", took, startGrace)
			}
			if got := h.sink.of(EvDeferred); len(got) != 1 || !strings.Contains(got[0], "A still open -> noted and deferred") {
				t.Errorf("deferred: %q", got)
			}
			if logged := h.logged(); !strings.Contains(logged, "A settled: idle 3m after it started, with the ticket still open") {
				t.Errorf("the log should say the grace settled it:\n%s", logged)
			}
		})
	})
}

// An adopted worker's hooks' record may still end with the Stop of the turn it asked in: a report
// from before it was adopted (or of unknown time) doesn't count, one since does.
func TestIdleSettledIgnoresReportsBeforeTheAdoption(t *testing.T) {
	since := time.Now()
	cases := []struct {
		name    string
		u       ToolUse
		since   time.Time
		settled bool
	}{
		{"stale Stop", ToolUse{Event: "Stop", At: since.Add(-time.Hour)}, since, false},
		{"Stop of unknown time", ToolUse{Event: "Stop"}, since, false},
		{"Stop since", ToolUse{Event: "Stop", At: since.Add(time.Second)}, since, true},
		{"not adopted", ToolUse{Event: "Stop", At: since.Add(-time.Hour)}, time.Time{}, true},
	}
	for _, c := range cases {
		hooks := &hookReporter{last: map[string]ToolUse{"wt": c.u}}
		o := &Loop{reporter: hooks}
		if settled, why := o.idleSettled("open", "wt", true, c.since, time.Minute, time.Second); settled != c.settled {
			t.Errorf("%s: settled = %v (%s), want %v", c.name, settled, why, c.settled)
		}
	}
}

// An asked worker that reports through hooks, adopted once its question is answered, is not
// settled by the Stop of the turn it asked in: it is waited on while it starts on the answer, idle
// with its ticket open, and settles at its own Stop hook.
func TestAdoptedWorkerSettlesAtItsOwnStopHook(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A",
			func(w *fakeWorker) AgentState {
				w.claim()
				w.ask("Q", "which way?")
				w.beads.set(w.id, "open")
				hooks.report(w.wt, "Stop")
				return "idle"
			},
			func(w *fakeWorker) AgentState { // once told the answer is in
				w.shows("idle")
				time.Sleep(time.Minute)          // many polls, within the start-up grace, before its first hook
				hooks.report(w.wt, "PreToolUse") // bd show Q
				w.claim()
				w.commit("a.txt")
				w.close()
				hooks.report(w.wt, "Stop")
				return "idle"
			})
		h.worker("B", func(w *fakeWorker) AgentState {
			time.Sleep(time.Minute)    // the stale Stop is older than the adoption
			w.beads.set("Q", "closed") // the maintainer answers meanwhile
			return finishes("b.txt")(w)
		})
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.of(EvDeferred); len(got) != 0 {
			t.Errorf("deferred: %q", got)
		}
		if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") {
			t.Errorf("main:\n%s", log)
		}
		if logged := h.logged(); strings.Count(logged, "A settled: Stop hook at ") != 2 {
			t.Errorf("both of A's turns should be settled by their own Stop hook:\n%s", logged)
		}
	})
}
