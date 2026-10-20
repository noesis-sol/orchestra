package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// tutti-nrg.7.7, from tutti's run of 2026-10-18: its worker finished the rebase it was handed back and
// said DONE as the account's usage limit hit, its turn ending on the limit; orchestra noticed only after
// the reset, 80 minutes on, once it had told the worker to carry on, and the ticket after it waited on it
// throughout. A worker at the limit once handed back whose work git shows done has given its answer: the
// branch is checked and merged without waiting for the limit to lift.
func TestAHandBackFinishedAsTheLimitHitsMergesBeforeTheReset(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		harn func(t *testing.T, dir string, hooks *hookReporter, reset time.Time) *harness
		done string
	}{
		{"a rebase resolved", func(t *testing.T, _ string, hooks *hookReporter, reset time.Time) *harness {
			h := newTimedHarness(t)
			h.cfg.Check = "true"
			h.cfg.ResolveConflicts = true
			h.mem.conflict("wt/A", "shared.txt")
			h.beads.add("A", "first", 1)
			h.worker("A", func(w *fakeWorker) AgentState {
				w.claim()
				w.git.commit("main", "landed on main: shared.txt", "shared.txt")
				w.commit("shared.txt")
				w.close()
				return "idle"
			}, finishesAtLimit(hooks, reset, resolvesInMemory(h.mem)))
			return h
		}, "its rebase has finished"},
		{"a check fixed", func(t *testing.T, dir string, hooks *hookReporter, reset time.Time) *harness {
			return checkHandBackHarness(t, dir, finishesAtLimit(hooks, reset, commitsFix(dir, "admin_test.go", true)))
		}, "its fix is committed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				hooks := &hookReporter{}
				reset := time.Now().Add(2 * time.Hour)
				h := tc.harn(t, t.TempDir(), hooks, reset)
				h.reporter = hooks
				h.cfg.UsageLimitWait = 6 * time.Hour
				o, code := h.run()
				if code != ExitOK || !strings.HasPrefix(o.Final(), "READY_EMPTY after 1 tickets") {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if got := closedIDs(h); !equal(got, []string{"A"}) {
					t.Fatalf("merged %v; want A\n%s", got, h.sink.text())
				}
				if at := h.sink.closedAt("A"); at.IsZero() || !at.Before(reset) {
					t.Errorf("A merged at %s, want before the reset (%s)", at, reset)
				}
				if got := h.herdr.typedTo("A"); !equal(got, []string{"prompt"}) {
					t.Errorf("typed to A: %q, want the hand-back alone", got)
				}
				logged := h.logged()
				for _, want := range []string{
					"USAGE_LIMIT: A's worker reached the usage limit",
					"A's worker is at the usage limit, but " + tc.done + ": wt/A is taken as it is, without " +
						"waiting for the limit to lift",
				} {
					if !strings.Contains(logged, want) {
						t.Errorf("the log lacks %q:\n%s", want, logged)
					}
				}
				for _, not := range []string{"told to carry on", "set aside", "CHECKS_FAILED"} {
					if strings.Contains(logged, not) {
						t.Errorf("the log has %q:\n%s", not, logged)
					}
				}
			})
		})
	}
}

// finishesAtLimit is a hand-back turn that does as does, then ends on the usage limit, which resets at
// reset: what it was handed back is done, but its worker shows limited.
func finishesAtLimit(hooks *hookReporter, reset time.Time, does behaviour) behaviour {
	return func(w *fakeWorker) AgentState {
		does(w)
		hooks.limit(w.wt, reset)
		return StateLimited
	}
}
