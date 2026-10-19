package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// A finished ticket whose branch is on top of Base already, nothing having merged while it ran, is
// checked before it merges as a rebased one is (see checkOnTop): its worker checks its own change,
// and the merge check tests everything, so no ticket reaches Base unchecked.

// failsOnlyFor is check, but passing in every worktree except ticket id's, which is named after it.
func failsOnlyFor(id, check string) string {
	return "(" + check + `) || test "$(basename "$PWD")" != '` + id + `'`
}

// changesNothing claims the ticket, commits a change and its undoing, which together change nothing,
// and closes it.
func changesNothing(w *fakeWorker) AgentState {
	w.claim()
	if err := w.git.commitIn(w.wt, w.id+": add a.txt and take it out again"); err != nil {
		w.t.Error(err)
	}
	w.close()
	return "idle"
}

// A passing check on a branch on top of main merges it, the line saying it was checked without a
// rebase, and the check runs once: the fast-forward after it brings in the very code checked.
func TestABranchOnTopOfBaseIsCheckedBeforeItMerges(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = countedCheck(dir, "")
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.write(dir, "fixed", "")
			return finishes("a.txt")(w)
		})
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"wt/A is on top of main already: checking it with '" + h.cfg.Check + "' before it merges\n",
			"'" + h.cfg.Check + "' passes on wt/A on top of main\n",
			"A closed",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		if strings.Contains(ev, "rebased wt/A") {
			t.Errorf("A should not be rebased:\n%s", ev)
		}
		if n := checkRuns(t, dir, "A"); n != 1 {
			t.Errorf("A's check ran %d times, want 1", n)
		}
		if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") {
			t.Errorf("A should merge:\n%s", log)
		}
	})
}

// A check failing on a branch on top of main is handed back to its worker to fix, as on a rebased
// one, without a list of what landed (nothing did); once fixed, the ticket merges with its fix.
func TestACheckFailingOnABranchOnTopOfBaseIsFixedByItsWorker(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = countedCheck(dir, `--- FAIL: TestAdmin (0.01s)\n`)
		h.cfg.CheckHandBacks = 2
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"), commitsFix(dir, "admin_test.go", true))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		pasted := h.herdr.pastedText("A")
		if len(pasted) != 1 {
			t.Fatalf("pasted %q to A, want the hand-back", pasted)
		}
		prompt := pasted[0]
		ev := h.sink.text()
		for _, want := range []string{
			"FIXING: '" + h.cfg.Check + "' fails on wt/A on top of main; handed back to its worker in tab tab1 " +
				"to fix (1 of 2, up to 20m)",
			"'" + h.cfg.Check + "' passes on wt/A as its worker fixed it",
			"A closed",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		for _, want := range []string{
			"Before wt/A merges into main, '" + h.cfg.Check + "' fails on it.",
			"--- FAIL: TestAdmin (0.01s)",
			"Fix wt/A so that the check passes.",
		} {
			if !strings.Contains(prompt, want) {
				t.Errorf("the hand-back lacks %q:\n%s", want, prompt)
			}
		}
		if strings.Contains(prompt, "landed") || strings.Contains(prompt, "rebased") {
			t.Errorf("nothing landed, and nothing was rebased:\n%s", prompt)
		}
		if strings.Contains(ev, "CHECKS_FAILED") {
			t.Errorf("events:\n%s", ev)
		}
		if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || !strings.Contains(log, "A: fix admin_test.go") {
			t.Errorf("A and its fix should merge:\n%s", log)
		}
		if n := checkRuns(t, dir, "A"); n != 2 {
			t.Errorf("A's check ran %d times, want 2", n)
		}
	})
}

// With hand-backs off, a check failing on a branch on top of main sets the ticket aside with
// CHECKS_FAILED, to be checked once more after main moves on, and main keeps none of it.
func TestACheckFailingOnABranchOnTopOfBaseSetsItAside(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = countedCheck(dir, "")
		h.cfg.CheckHandBacks = 0
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		want := "CHECKS_FAILED: A closed, but '" + h.cfg.Check + "' fails on wt/A on top of main; worktree "
		if !strings.Contains(ev, want) || !strings.Contains(ev, "checked once more if main moves on in this run\n") {
			t.Errorf("events lack %q, to be checked once more:\n%s", want, ev)
		}
		if aside := o.asideList(); !equal(aside, []string{"A"}) {
			t.Errorf("set aside %v, want A", aside)
		}
		if !unmergedLabel(t, h, "A") {
			t.Error("A should be labelled unmerged")
		}
		if strings.Contains(h.mainLog(), "A: add") {
			t.Errorf("main must not change:\n%s", h.mainLog())
		}
		if n := checkRuns(t, dir, "A"); n != 1 {
			t.Errorf("A's check ran %d times, want 1", n)
		}
	})
}

// A branch on top of main whose commits change nothing has nothing to merge: it is closed as
// unchanged without a check.
func TestABranchOnTopOfBaseThatChangesNothingIsNotChecked(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = countedCheck(dir, "")
		h.beads.add("A", "first", 1)
		h.worker("A", changesNothing)
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		if !strings.Contains(ev, "A closed with no change: the commits on wt/A, taken together, change nothing") {
			t.Errorf("want A closed with no change:\n%s", ev)
		}
		if n := checkRuns(t, dir, "A"); n != 0 {
			t.Errorf("A's check ran %d times, want none", n)
		}
		if strings.Contains(ev, "CHECKS_FAILED") || strings.Contains(ev, "checking it with") {
			t.Errorf("A should not be checked:\n%s", ev)
		}
	})
}
