package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// failOn reports, as the hooks of the worker in wt do, that its turn ended just now on an API error
// other than the usage limit, of the kind StopFailure's error names: the StopFailure ends the turn as
// a Stop does (see claude.Reporter.LastToolUse).
func (r *hookReporter) failOn(wt, kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		r.last = map[string]ToolUse{}
	}
	now := time.Now()
	r.last[wt] = ToolUse{Event: "StopFailure", At: now, Stopped: now, Failure: kind}
}

// replaceFailure has an agent Claude Code forks at the end of the turn of the worker in wt run its
// PreToolUse hook after the worker's StopFailure, as replaceStop does after a Stop: the record of the
// tool use replaces the StopFailure's, which the turn's record still tells.
func (r *hookReporter) replaceFailure(wt string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	f := r.last[wt]
	r.last[wt] = ToolUse{Event: "PreToolUse", Tool: "Read", At: time.Now(), Stopped: f.Stopped, Failure: f.Failure,
		Session: "s1"}
}

// endsOnServerError is a worker turn that ends on a server error, its ticket claimed if claims and
// closed with a commit if closes, the record of its StopFailure then replaced by a fork's tool use if
// replaced.
func endsOnServerError(hooks *hookReporter, claims, closes, replaced bool) behaviour {
	return func(w *fakeWorker) AgentState {
		hooks.report(w.wt, "PreToolUse")
		if claims {
			w.claim()
		}
		if closes {
			w.commit("a.txt")
			w.close()
		}
		hooks.failOn(w.wt, "server_error")
		if replaced {
			hooks.replaceFailure(w.wt)
		}
		return "idle"
	}
}

// A worker whose turn ends on an API error other than the usage limit, with its ticket still in
// progress, is told to continue at once, as at a Stop, rather than after the idle grace; so is one
// whose StopFailure a fork's tool use replaced.
func TestWorkerWhoseTurnEndsOnAnAPIErrorIsToldToContinue(t *testing.T) {
	t.Parallel()
	for name, replaced := range map[string]bool{"StopFailure": false, "StopFailure replaced": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				hooks := &hookReporter{}
				h.reporter = hooks
				h.beads.add("A", "first", 1)
				h.worker("A", endsOnServerError(hooks, true, false, replaced), func(w *fakeWorker) AgentState {
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
				if logged := h.logged(); !strings.Contains(logged,
					"A's worker ended its turn with the ticket still in progress; told it to continue (1 of 2)") {
					t.Errorf("the log should say it was told to continue:\n%s", logged)
				}
			})
		})
	}
}

// A worker whose turn ends on an API error once it has closed its ticket settles at its StopFailure,
// which the log names with the error's kind, rather than after the idle grace; so does one whose
// StopFailure a fork's tool use replaced, the log saying who wrote that record.
func TestWorkerSettlesAtAStopFailure(t *testing.T) {
	t.Parallel()
	for name, replaced := range map[string]bool{"StopFailure": false, "StopFailure replaced": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				hooks := &hookReporter{}
				h.reporter = hooks
				h.beads.add("A", "first", 1)
				h.worker("A", endsOnServerError(hooks, true, true, replaced))
				start := time.Now()
				o, code := h.run()
				if code != ExitOK {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if took := time.Since(start); took >= idleGrace {
					t.Errorf("settled after %s, want at its StopFailure hook", took)
				}
				if !strings.Contains(h.mainLog(), "A: add a.txt") {
					t.Errorf("main:\n%s", h.mainLog())
				}
				wants := []string{"A settled: StopFailure hook (server_error) at "}
				if replaced {
					wants = append(wants, "; then a PreToolUse record with no turn of its own, written at ")
				}
				logged := h.logged()
				for _, want := range wants {
					if !strings.Contains(logged, want) {
						t.Errorf("the log lacks %q:\n%s", want, logged)
					}
				}
			})
		})
	}
}

// A turn that ended on the usage limit is no turn's end the loop acts on, though the terminal shows
// its worker idle rather than limited (Claude Code's words of it gone from the screen): the worker
// isn't told to continue, and with its ticket in progress the run pauses for it after the idle grace,
// as for any idle worker whose turn goes on.
func TestAUsageLimitShownIdleEndsNoTurn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			hooks.report(w.wt, "PreToolUse")
			w.claim()
			hooks.limit(w.wt, time.Time{})
			return "idle"
		})
		o, code := h.run()
		if code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: A still in_progress") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.herdr.pastedTo(); len(got) != 0 {
			t.Errorf("messages pasted to %v, want none", got)
		}
		if logged := h.logged(); !strings.Contains(logged, "A settled: idle for 10m with the ticket still in progress") {
			t.Errorf("the log should say it settled after the idle grace:\n%s", logged)
		}
	})
}

// turnEnded counts a StopFailure as the turn's end, as it does a Stop, and one a fork's tool use
// replaced, and turnEndName names it with its error's kind; not one on the usage limit, though a turn's
// end is told with it, nor one while a subagent runs.
func TestTurnEndedAtAStopFailure(t *testing.T) {
	t.Parallel()
	failed := time.Now()
	limit := UsageLimit{At: failed, Message: "You've hit your session limit · resets 7pm (Europe/Athens)"}
	for _, c := range []struct {
		name            string
		u               ToolUse
		subagents       bool
		ended, replaced bool
		named           string
	}{
		{"server error", ToolUse{Event: "StopFailure", At: failed, Stopped: failed, Failure: "server_error"},
			false, true, false, "StopFailure hook (server_error) at "},
		{"no kind named", ToolUse{Event: "StopFailure", At: failed, Stopped: failed},
			false, true, false, "StopFailure hook at "},
		{"no turn's end told", ToolUse{Event: "StopFailure", At: failed, Failure: "server_error"},
			false, true, false, "StopFailure hook (server_error) at "},
		{"replaced", ToolUse{Event: "PreToolUse", At: failed.Add(time.Second), Stopped: failed, Failure: "server_error"},
			false, true, true, "StopFailure hook (server_error) at "},
		{"subagent running", ToolUse{Event: "StopFailure", At: failed, Stopped: failed, Failure: "server_error"},
			true, false, false, ""},
		{"usage limit", ToolUse{Event: "StopFailure", At: failed, Failure: "rate_limit", Limit: limit},
			false, false, false, ""},
		{"usage limit replaced", ToolUse{Event: "PreToolUse", At: failed.Add(time.Second), Failure: "rate_limit",
			Limit: limit}, false, false, false, ""},
		{"usage limit with a turn's end", ToolUse{Event: "StopFailure", At: failed, Stopped: failed,
			Failure: "rate_limit", Limit: limit}, false, false, false, ""},
	} {
		h := hooksSeen{use: c.u, ok: true}
		if c.subagents {
			h.rec = HookRecord{Subagents: 1}
		}
		at, ended, replaced := turnEnded(h, failed.Add(2*time.Second))
		if ended != c.ended || replaced != c.replaced || ended && !at.Equal(failed) {
			t.Errorf("%s: ended %v at %s, replaced %v; want %v at %s, %v", c.name, ended, at, replaced,
				c.ended, failed, c.replaced)
		}
		if !ended {
			continue
		}
		if name, want := turnEndName(h, at), c.named+failed.Format("15:04:05"); name != want {
			t.Errorf("%s: named %q, want %q", c.name, name, want)
		}
	}
}

// A worker whose ticket isn't in progress, shown idle after a turn that ended on the usage limit,
// settles after the idle grace, as one whose turn goes on, and the log says it was the limit.
func TestUsageLimitShownIdleSettlesAfterTheIdleGrace(t *testing.T) {
	t.Parallel()
	failed := time.Now()
	limit := UsageLimit{At: failed, Message: "You've hit your session limit · resets 7pm (Europe/Athens)"}
	h := hooksSeen{use: ToolUse{Event: "StopFailure", At: failed, Failure: "rate_limit", Limit: limit}, ok: true}
	if settled, why := idleSettled("closed", h, time.Time{}, failed.Add(time.Minute), time.Minute, time.Hour); settled {
		t.Errorf("settled a minute after the limit: %s", why)
	}
	settled, why := idleSettled("closed", h, time.Time{}, failed.Add(idleGrace), idleGrace, time.Hour)
	want := "idle for 10m after its turn ended on the usage limit at " + failed.Format("15:04:05") + " (" +
		limit.Message + ")"
	if !settled || why != want {
		t.Errorf("after the idle grace: settled %v (%s), want %q", settled, why, want)
	}
}
