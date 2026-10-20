package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// A worker handed its finished ticket back that reaches the account's usage limit, or is at it
// already, waits it out with the run's hold, as a worker on its ticket does (see waitResolved).

// limitedTerminal is the fake terminal, but for ticket id's worker, which shows limited where the fake
// shows it idle once limited says so, until it is next given a prompt, or until until (zero: none): a
// worker at the usage limit by the time its ticket is handed back.
type limitedTerminal struct {
	*fakeHerdr
	id      string
	limited func() bool
	until   time.Time

	mu       sync.Mutex
	prompted bool
}

func (l *limitedTerminal) Status(ctx context.Context, name string) (AgentState, error) {
	st, err := l.fakeHerdr.Status(ctx, name)
	l.mu.Lock()
	defer l.mu.Unlock()
	if err == nil && st == StateIdle && name == l.id && !l.prompted && l.limited() &&
		(l.until.IsZero() || time.Now().Before(l.until)) {
		return StateLimited, nil
	}
	return st, err
}

func (l *limitedTerminal) Prompt(ctx context.Context, name, prompt string) error {
	if name == l.id {
		l.mu.Lock()
		l.prompted = true
		l.mu.Unlock()
	}
	return l.fakeHerdr.Prompt(ctx, name, prompt)
}

// checkRan reports whether countedCheck, writing to dir, ran for ticket id.
func checkRan(dir, id string) bool {
	_, err := os.Stat(filepath.Join(dir, id+".runs"))
	return err == nil
}

// A worker handed its failing check back that reaches the usage limit holds the run: no new ticket
// starts, and it isn't set aside when the hand-back's 20 minutes are up, as the time it waits doesn't
// count. A minute after the reset it is told to carry on, Escape first, and its fix merges.
func TestAWorkerLimitedInAHandBackHoldsTheRunUntilTheReset(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		hooks := &hookReporter{}
		var reset time.Time
		var h *harness
		h = checkHandBackHarness(t, dir, func(w *fakeWorker) AgentState {
			readyOnceHeld(h, "B", "ready while the run holds", 2)
			return reachesLimit(hooks, false, reset)(w)
		}, commitsFix(dir, "admin_test.go", true))
		h.reporter = hooks
		h.cfg.Check = failsOnlyFor("A", h.cfg.Check) // B, on top of main, passes
		h.cfg.Concurrency = 2
		h.cfg.UsageLimitWait = 6 * time.Hour
		h.worker("B", finishes("b.txt"))
		start := time.Now()
		reset = start.Add(2 * time.Hour)
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		for _, f := range []string{"A: add a.txt", "A: fix admin_test.go", "B: add b.txt"} {
			if !strings.Contains(h.mainLog(), f) {
				t.Errorf("main lacks %q:\n%s", f, h.mainLog())
			}
		}
		if b := h.sink.dispatchedAt("B"); b.Before(reset.Add(resetGrace)) {
			t.Errorf("B started at %s, before the reset and its minute (%s)", b, reset.Add(resetGrace))
		}
		if got := h.herdr.typedTo("A"); !equal(got, []string{"prompt", "esc", "prompt"}) {
			t.Errorf("typed to A: %q, want the hand-back, then Escape and one message", got)
		}
		if got := h.herdr.pastedText("A"); len(got) != 2 || !strings.Contains(got[1], "usage limit has reset. Carry on with A") {
			t.Errorf("pasted to A: %q", got)
		}
		logged := h.logged()
		for _, want := range []string{
			"USAGE_LIMIT: A's worker reached the usage limit; it resets at " + ResetLabel(reset, start),
			"USAGE_LIMIT lifted: the limit reset at " + ResetLabel(reset, start),
			"A's worker was told to carry on after the usage limit",
			"'" + h.cfg.Check + "' passes on wt/A as its worker fixed it",
		} {
			if !strings.Contains(logged, want) {
				t.Errorf("the log lacks %q:\n%s", want, logged)
			}
		}
		for _, not := range []string{"was still limited", "CHECKS_FAILED", "set aside"} {
			if strings.Contains(logged, not) {
				t.Errorf("the log has %q:\n%s", not, logged)
			}
		}
		if held := h.sink.eventsOf(EvLimited); len(held) != 1 || !held[0].Reset.Equal(reset) {
			t.Errorf("usage limit events: %+v", held)
		}
	})
}

// A worker at the usage limit as its failing check is handed back isn't passed over: the hand-back
// joins the run's hold, which no new ticket starts through, and is given where the worker would be
// told to carry on, Escape first, or as soon as it goes idle by itself (Claude Code's own continue
// having carried it past the limit). Its fix merges.
func TestAHandBackToALimitedWorkerWaitsForTheReset(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		idle   time.Duration // after the start, when it goes idle by itself; zero: never
		typed  []string
		handed time.Duration // after the start, the hand-back is given no sooner
	}{
		{"told as the limit resets", 0, []string{"esc", "prompt"}, 2*time.Hour + resetGrace},
		{"idle by itself", 2 * time.Hour, []string{"prompt"}, 2 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				dir := t.TempDir()
				hooks := &hookReporter{}
				start := time.Now()
				reset := start.Add(2 * time.Hour)
				h := newTimedHarness(t)
				h.cfg.Check = failsOnlyFor("A", countedCheck(dir, `--- FAIL: TestAdmin (0.01s)\n`))
				h.cfg.CheckHandBacks = 2
				h.cfg.Concurrency = 2
				h.cfg.UsageLimitWait = 6 * time.Hour
				h.reporter = hooks
				h.beads.add("A", "first", 1)
				h.worker("A", func(w *fakeWorker) AgentState {
					st := landsMeanwhile("a.txt")(w)
					hooks.limit(w.wt, reset) // its turn ended on the limit as it closed its ticket
					readyOnceHeld(h, "B", "ready while the run holds", 2)
					return st
				}, commitsFix(dir, "admin_test.go", true))
				h.worker("B", finishes("b.txt"))
				var pasted pasteTimes
				h.herdr.onPrompt = pasted.on
				o := h.loop()
				lt := &limitedTerminal{fakeHerdr: h.herdr, id: "A", limited: func() bool { return checkRan(dir, "A") }}
				if tc.idle > 0 {
					lt.until = start.Add(tc.idle)
				}
				o.agents = lt
				code := o.Run(context.Background())
				if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				for _, f := range []string{"A: add a.txt", "A: fix admin_test.go", "B: add b.txt"} {
					if !strings.Contains(h.mainLog(), f) {
						t.Errorf("main lacks %q:\n%s", f, h.mainLog())
					}
				}
				if got := h.herdr.typedTo("A"); !equal(got, tc.typed) {
					t.Errorf("typed to A: %q, want %q", got, tc.typed)
				}
				if got := h.herdr.pastedText("A"); len(got) != 1 || !strings.Contains(got[0], "Fix wt/A so that the check passes") {
					t.Errorf("pasted to A: %q, want the hand-back alone", got)
				}
				if at := pasted.all(); len(at) == 0 || at[0].Before(start.Add(tc.handed)) {
					t.Errorf("handed back at %v, want no sooner than %s", at, start.Add(tc.handed))
				}
				if b := h.sink.dispatchedAt("B"); b.Before(reset) {
					t.Errorf("B started at %s, before the reset (%s)", b, reset)
				}
				logged := h.logged()
				for _, want := range []string{
					"FIXING: '" + h.cfg.Check + "' fails on wt/A rebased onto main; handed back to its worker in tab tab1 " +
						"to fix (1 of 2, up to 20m)",
					"A's worker is at the usage limit: it is handed back once the limit lifts, the wait not counting " +
						"toward the 20m",
					"USAGE_LIMIT: A's worker reached the usage limit; it resets at " + ResetLabel(reset, start),
					"'" + h.cfg.Check + "' passes on wt/A as its worker fixed it",
				} {
					if !strings.Contains(logged, want) {
						t.Errorf("the log lacks %q:\n%s", want, logged)
					}
				}
				for _, not := range []string{"not handed back", "CHECKS_FAILED", "its worker is limited"} {
					if strings.Contains(logged, not) {
						t.Errorf("the log has %q:\n%s", not, logged)
					}
				}
			})
		})
	}
}

// A hold that lasts usage_limit_wait while a worker is handed its failing check back stops the run
// with USAGE_LIMIT, as it does for a worker on its ticket: the ticket isn't set aside, and its note
// says what is left to finish by hand.
func TestAHoldGivingUpDuringAHandBackStopsTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		hooks := &hookReporter{}
		reset := time.Now().Add(2 * time.Hour)
		h := checkHandBackHarness(t, dir, reachesLimit(hooks, false, reset))
		h.reporter = hooks
		h.cfg.UsageLimitWait = time.Hour
		o, code := h.run()
		if code != ExitUsageLimit || h.sink.stopped() != stopUsageLimit {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		left := "; its failing check, handed back to fix, is left to its worker: once it has committed the fix, " +
			"run the check and merge the branch by hand"
		if !strings.Contains(o.Final(), left) {
			t.Errorf("final %q lacks %q", o.Final(), left)
		}
		notes := h.beads.notesOf("A")
		if !strings.Contains(notes, "reached the account's usage limit") || !strings.Contains(notes, left+".") {
			t.Errorf("A's notes:\n%s", notes)
		}
		logged := h.logged()
		for _, not := range []string{"CHECKS_FAILED", "was still limited"} {
			if strings.Contains(logged, not) {
				t.Errorf("the log has %q:\n%s", not, logged)
			}
		}
		if strings.Contains(h.mainLog(), "A: add") {
			t.Errorf("main must not change:\n%s", h.mainLog())
		}
		if !unmergedLabel(t, h, "A") {
			t.Error("A, left handed back, should be labelled unmerged")
		}
	})
}
