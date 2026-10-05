package dispatch

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// Handing a check that fails on a finished ticket's rebased branch back to its worker to fix, in
// whole runs (see checkRebased).

// checkHandBackHarness is a timed run whose ticket A's check fails once main has moved on, until dir
// holds the file fixed, with failed checks handed back to its worker; A's worker closes it as
// landsMeanwhile does and then behaves as fixes, one per hand-back.
func checkHandBackHarness(t *testing.T, dir string, fixes ...behaviour) *harness {
	t.Helper()
	h := newTimedHarness(t)
	h.cfg.Check = countedCheck(dir, `--- FAIL: TestAdmin (0.01s)\n`)
	h.cfg.CheckHandBacks = 2
	h.beads.add("A", "first", 1)
	h.worker("A", append([]behaviour{landsMeanwhile("a.txt")}, fixes...)...)
	return h
}

// commitsFix commits file as the fix of the check, naming the ticket, and makes the check pass when
// passes is set.
func commitsFix(dir, file string, passes bool) behaviour {
	return func(w *fakeWorker) AgentState {
		if passes {
			w.write(dir, "fixed", "")
		}
		w.commitAs(file, w.id+": fix "+file)
		return "idle"
	}
}

// orchestra-fgil, from the ancient-chat run of 2026-10-05: kwh.3 changed the admin check, and h65.6,
// merged meanwhile, added tests that mock an admin the old way, so the check failed on kwh.3 rebased
// onto it. Its worker, idle in its tab, is handed the failure, commits the fix and the ticket merges.
func TestACheckFailingOnTheRebasedBranchIsFixedByItsWorker(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		h := checkHandBackHarness(t, dir, commitsFix(dir, "admin_test.go", true))
		var shown Status
		h.herdr.onPrompt = func(id string) { shown = h.sink.lastStatus(id) }
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"FIXING: '" + h.cfg.Check + "' fails on wt/A rebased onto main; handed back to its worker in tab tab1 " +
				"to fix (1 of 2, up to 20m)",
			"'" + h.cfg.Check + "' passes on wt/A as its worker fixed it",
			"A closed",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		if strings.Contains(ev, "CHECKS_FAILED") {
			t.Errorf("events:\n%s", ev)
		}
		if !shown.Fixing || shown.Resolving || shown.Agent != StateIdle {
			t.Errorf("when the hand-back was pasted the dashboard showed %+v, want A idle and fixing", shown)
		}
		if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || !strings.Contains(log, "A: fix admin_test.go") {
			t.Errorf("A and its fix should merge:\n%s", log)
		}
		if n := checkRuns(t, dir, "A"); n != 2 {
			t.Errorf("A's check ran %d times, want 2", n)
		}
		if !equal(h.herdr.pastedTo(), []string{"A"}) {
			t.Errorf("prompts pasted to %v, want the hand-back to A", h.herdr.pastedTo())
		}
		if unmergedLabel(t, h, "A") {
			t.Error("A should not be labelled unmerged")
		}
	})
}

// A worker that doesn't fix the check is handed it back up to the cap, and the ticket is then set
// aside as before, with a note naming each attempt.
func TestACheckItsWorkerCannotFixIsSetAsideAfterTheCap(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		h := checkHandBackHarness(t, dir, commitsFix(dir, "try1.txt", false), commitsFix(dir, "try2.txt", false))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		check := "'" + h.cfg.Check + "'"
		ev := h.sink.text()
		for _, want := range []string{
			"to fix (1 of 2, up to 20m)",
			"to fix (2 of 2, up to 20m)",
			"CHECKS_FAILED: A closed, but " + check + " fails on wt/A rebased onto main, after it was handed back " +
				"to its worker to fix twice (see its notes); worktree ",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		notes := h.beads.notesOf("A")
		for _, want := range []string{
			"Orchestra: " + check + " fails on wt/A rebased onto main (output is in ",
			"; it was handed back to its worker to fix twice: (1) its worker committed 1 commit, but " + check +
				" fails still (output is in ",
			"; (2) its worker committed 1 commit, but " + check + " fails still (output is in ",
			"; not handed back again: it was handed back to its worker twice already; so A was set aside for review.",
		} {
			if !strings.Contains(notes, want) {
				t.Errorf("notes lack %q:\n%s", want, notes)
			}
		}
		if n := checkRuns(t, dir, "A"); n != 3 {
			t.Errorf("A's check ran %d times, want 3", n)
		}
		if strings.Contains(h.mainLog(), "A: add") {
			t.Errorf("main must not change:\n%s", h.mainLog())
		}
		if !unmergedLabel(t, h, "A") {
			t.Error("A should be labelled unmerged")
		}
	})
}

// A fix orchestra can't accept, here a commit that doesn't name the ticket, is undone and the ticket
// set aside, without a second hand-back.
func TestAFixCommittedWithoutTheTicketIsUndone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		h := checkHandBackHarness(t, dir, func(w *fakeWorker) AgentState {
			w.write(dir, "fixed", "")
			w.commitAs("fix.txt", "fix the test")
			return "idle"
		})
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		notes := h.beads.notesOf("A")
		if !strings.Contains(notes, "fix once: (1) its worker's commit ") ||
			!strings.Contains(notes, " fix the test does not name A; wt/A was reset to ") {
			t.Errorf("notes:\n%s", notes)
		}
		if n := strings.Count(h.sink.text(), "FIXING:"); n != 1 {
			t.Errorf("%d hand-backs, want 1:\n%s", n, h.sink.text())
		}
		if log := h.mem.log("wt/A"); strings.Contains(log, "fix the test") || !strings.Contains(log, "A: add a.txt") {
			t.Errorf("wt/A should be back where its check failed:\n%s", log)
		}
	})
}

// No check is handed back while the run winds down, nor to a worker gone from its tab; the
// CHECKS_FAILED line says why.
func TestACheckIsNotHandedBackWhenItCannotBe(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		drain bool
		state AgentState
		why   string
	}{
		{"winding down", true, "idle", "the run is winding down"},
		{"worker gone", false, "gone", "its worker is gone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				dir := t.TempDir()
				h := newTimedHarness(t)
				h.cfg.Check = countedCheck(dir, "")
				h.cfg.CheckHandBacks = 2
				h.beads.add("A", "first", 1)
				var o *Loop
				h.worker("A", func(w *fakeWorker) AgentState {
					if tc.drain {
						o.Drain("from the dashboard")
						time.Sleep(time.Second) // heard before A is done
					}
					landsMeanwhile("a.txt")(w)
					return tc.state
				})
				o = h.loop()
				o.Run(t.Context())
				ev := h.sink.text()
				if !strings.Contains(ev, "CHECKS_FAILED: A closed, but '"+h.cfg.Check+"' fails on wt/A rebased onto main; "+
					"not handed back to its worker: "+tc.why+"; worktree ") || strings.Contains(ev, "FIXING") {
					t.Errorf("events:\n%s", ev)
				}
				if p := h.herdr.pastedTo(); len(p) > 0 {
					t.Errorf("nothing should be pasted: %v", p)
				}
			})
		})
	}
}

// A rebase whose conflicts its worker resolved, but whose check then fails, is handed back once more
// to fix, and merges once fixed.
func TestAResolvedRebaseWhoseCheckFailsIsFixedByItsWorker(t *testing.T) {
	t.Parallel()
	h := conflictHarness(t)
	h.cfg.CheckHandBacks = 2
	h.beads.add("A", "first", 1)
	h.worker("A", conflicting(h.repo), resolvesWith("<<<<<<< ours\nmain\n=======\nA\n>>>>>>> theirs\n"),
		func(w *fakeWorker) AgentState {
			w.write(w.wt, "shared.txt", "main\nA\n")
			w.gitIn(w.wt, "commit", "-q", "-am", "A: drop the conflict markers")
			return "idle"
		})
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	ev := h.sink.text()
	for _, want := range []string{
		"RESOLVING: wt/A conflicts with main in shared.txt",
		"FIXING: '! grep -q '<<<<<<<' shared.txt' fails on wt/A rebased onto main; handed back to its worker",
		"'! grep -q '<<<<<<<' shared.txt' passes on wt/A as its worker fixed it",
		"A closed",
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("events lack %q:\n%s", want, ev)
		}
	}
	if got := read(t, filepath.Join(h.repo, "shared.txt")); got != "main\nA\n" {
		t.Errorf("shared.txt on main = %q", got)
	}
	if log := h.mainLog(); !strings.Contains(log, "A: drop the conflict markers") {
		t.Errorf("history:\n%s", log)
	}
}

// A fix left uncommitted is rejected: the resolved rebase is set aside as a failed resolution, its
// worktree left as the worker left it, and the note says what was tried.
func TestAFixLeftUncommittedSetsTheResolvedTicketAside(t *testing.T) {
	t.Parallel()
	h := conflictHarness(t)
	h.cfg.CheckHandBacks = 2
	h.beads.add("A", "first", 1)
	h.worker("A", conflicting(h.repo), resolvesWith("<<<<<<< ours\nmain\n=======\nA\n>>>>>>> theirs\n"),
		func(w *fakeWorker) AgentState {
			w.write(w.wt, "shared.txt", "main\nA\n")
			return "idle"
		})
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	wt := h.worktree("A")
	want := "but '! grep -q '<<<<<<<' shared.txt' fails on the resolved wt/A (output is in " +
		filepath.Join(wt, ".orchestra", "run", "check.log") + "), and was handed back to its worker to fix once: " +
		"(1) its worker left uncommitted changes in " + wt + "; " + wt + " is left as its worker left it"
	if ev := h.sink.text(); !strings.Contains(ev, "MERGE_CONFLICT: A closed") || !strings.Contains(ev, want) {
		t.Errorf("events lack %q:\n%s", want, ev)
	}
	if n := h.beads.notesOf("A"); !strings.Contains(n, "its worker was asked to resolve the rebase, "+want) {
		t.Errorf("notes:\n%s", n)
	}
	if st := h.git(wt, "status", "--porcelain"); !strings.Contains(st, "shared.txt") {
		t.Errorf("the worker's change should be left: %q", st)
	}
	if got := read(t, filepath.Join(h.repo, "shared.txt")); got != "main\n" {
		t.Errorf("main must not change: shared.txt = %q", got)
	}
}

// The worker is told what failed, where the whole output is, what landed on Base and what to do.
func TestTheFixPromptSaysWhatFailedAndWhatLanded(t *testing.T) {
	t.Parallel()
	o := &Loop{cfg: Config{Base: "main", Check: "scripts/check.sh"}, history: landedHistory{
		"aaaaaaaaaaaa..bbbbbbbbbbbb": "1234567 h65.6: test the admin page\n89abcde h65.7: more tests\n"}}
	o.checkSaid = map[string]checkFail{"A": {said: []string{"--- FAIL: TestAdmin (0.01s)", "FAIL\tm/admin\t0.1s"}}}
	r := rebaseStop{worker: worker{id: "A", br: "wt/A"}, onto: "bbbbbbbbbbbb", head: "aaaaaaaaaaaa"}
	got := o.fixPrompt(context.Background(), r, checked{err: errors.New("exit status 1"), output: "/wt/A/.orchestra/run/check.log"})
	for _, want := range []string{
		"main moved on while you worked, and wt/A was rebased onto it; now 'scripts/check.sh' fails on it. " +
			"Its whole output is in /wt/A/.orchestra/run/check.log; these lines say what failed:\n" +
			"--- FAIL: TestAdmin (0.01s)\nFAIL\tm/admin\t0.1s\n\n",
		"What landed on main since your branch was cut (git log --oneline aaaaaaaaaa..bbbbbbbbbb):\n" +
			"1234567 h65.6: test the admin page\n89abcde h65.7: more tests\n\n",
		"Fix wt/A so that the check passes, keeping the intent of both your change and what landed on main. ",
		"then commit the fix on wt/A with A in the commit message. ",
		"no rebase, no amending or rewriting of commits",
		"Say DONE when the fix is committed.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt lacks %q:\n%s", want, got)
		}
	}
}

// landedHistory gives git log --oneline for the ranges it holds.
type landedHistory map[string]string

func (l landedHistory) OneLineLog(ctx context.Context, dir, revs string) string { return l[revs] }
func (landedHistory) ShortStatus(ctx context.Context, worktree string) string   { return "" }
func (landedHistory) DiffStat(ctx context.Context, worktree string) string      { return "" }
func (landedHistory) Subjects(ctx context.Context, repo, revs string) string    { return "" }
func (landedHistory) ChangedFiles(ctx context.Context, repo, from, to string) []string {
	return nil
}

// Ctrl+C during a check hand-back says what is left to do: the worker's fix is checked and merged
// by hand.
func TestTheInterruptLineSaysAWorkerIsFixingItsCheck(t *testing.T) {
	t.Parallel()
	got := InterruptLine("with Ctrl+C", []Status{{Ticket: "A", Tab: "tab1", Fixing: true}})
	if !strings.Contains(got, "A (tab tab1, fixing its check: once its worker has committed the fix, "+
		"run the check and merge it)") {
		t.Errorf("InterruptLine = %q", got)
	}
}
