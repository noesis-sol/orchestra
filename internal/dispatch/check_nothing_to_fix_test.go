package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// A check hand-back whose worker finds nothing to fix and commits nothing: the check runs once more
// (see checkRebased).

// commitsNothing leaves the branch as it was, and makes the check pass when passes is set, as a
// failure that didn't come from the change (a test timing out on a loaded machine) would.
func commitsNothing(dir string, passes bool) behaviour {
	return func(w *fakeWorker) AgentState {
		if passes {
			w.write(dir, "fixed", "")
		}
		return "idle"
	}
}

// orchestra-2jwy, from tutti's run of 2026-10-19: the check failed on nrg.8.6 rebased onto main with
// timeouts far from its change; its worker reran it, saw it pass and committed nothing. The check runs
// again, passes, and the ticket merges.
func TestACheckHandBackWithNothingCommittedIsCheckedAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		h := checkHandBackHarness(t, dir, commitsNothing(dir, true))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"to fix (1 of 2, up to 20m)",
			"'" + h.cfg.Check + "' passes on wt/A on a second run; its worker committed nothing",
			"A closed",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		if strings.Contains(ev, "CHECKS_FAILED") {
			t.Errorf("events:\n%s", ev)
		}
		if n := checkRuns(t, dir, "A"); n != 2 {
			t.Errorf("A's check ran %d times, want 2", n)
		}
		if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") {
			t.Errorf("A should merge:\n%s", log)
		}
		if unmergedLabel(t, h, "A") {
			t.Error("A should not be labelled unmerged")
		}
	})
}

// A check that fails again after its worker committed nothing sets the ticket aside without a second
// hand-back: its worker found nothing to fix.
func TestACheckFailingAgainWithNothingCommittedIsSetAside(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		h := checkHandBackHarness(t, dir, commitsNothing(dir, false), commitsFix(dir, "never.txt", true))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		check := "'" + h.cfg.Check + "'"
		ev := h.sink.text()
		if n := strings.Count(ev, "FIXING:"); n != 1 {
			t.Errorf("%d hand-backs, want 1:\n%s", n, ev)
		}
		if want := "CHECKS_FAILED: A closed, but " + check + " fails on wt/A rebased onto main, after it was " +
			"handed back to its worker to fix once (see its notes); worktree "; !strings.Contains(ev, want) {
			t.Errorf("events lack %q:\n%s", want, ev)
		}
		notes := h.beads.notesOf("A")
		for _, want := range []string{
			"; it was handed back to its worker to fix once: (1) its worker committed nothing, and " + check +
				" fails again (output is in ",
			"; not handed back again: its worker found nothing to fix; so A was set aside for review.",
		} {
			if !strings.Contains(notes, want) {
				t.Errorf("notes lack %q:\n%s", want, notes)
			}
		}
		if n := checkRuns(t, dir, "A"); n != 2 {
			t.Errorf("A's check ran %d times, want 2", n)
		}
		if strings.Contains(h.mainLog(), "A: add") {
			t.Errorf("main must not change:\n%s", h.mainLog())
		}
		if !unmergedLabel(t, h, "A") {
			t.Error("A should be labelled unmerged")
		}
	})
}
