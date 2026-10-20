package dispatch

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
)

// orchestra-ljuz: a worker handed back its ticket, to fix a failing check or to resolve a rebase, may
// leave its worktree on another branch or a detached HEAD, against its rules. The check runs on what
// the worktree has checked out, and the merge then fast-forwarded Base to the ticket's branch, which
// was never checked. What the worker left is rejected instead, and the branch it isn't on left alone.

// fixesThenSwitches commits a fix of the check on the ticket's branch, naming it, then switches the
// worktree to branch ("" detaches its HEAD) and makes the check pass there.
func fixesThenSwitches(dir, branch string) behaviour {
	return func(w *fakeWorker) AgentState {
		w.commitAs("fix.txt", w.id+": fix the check")
		w.git.switchTo(w.wt, branch)
		w.write(dir, "fixed", "")
		return "idle"
	}
}

func TestAFixLeftOffItsBranchIsRejected(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, branch, on string }{
		{"another branch", "other", "other checked out"},
		{"detached", "", "a detached HEAD"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				dir := t.TempDir()
				h := checkHandBackHarness(t, dir, fixesThenSwitches(dir, tc.branch))
				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				wt := h.worktree("A")
				if ev, want := h.sink.text(), "CHECKS_FAILED: A closed, but '"+h.cfg.Check+"' fails on wt/A rebased "+
					"onto main, after it was handed back to its worker to fix once"; !strings.Contains(ev, want) {
					t.Errorf("events lack %q:\n%s", want, ev)
				}
				fixed := h.mem.head("wt/A")
				notes := h.beads.notesOf("A")
				if want := "(1) its worker left " + wt + " with " + tc.on + ", not wt/A; wt/A is left at " +
					short(fixed) + ", as " + wt + " has " + tc.on; !strings.Contains(notes, want) {
					t.Errorf("notes lack %q:\n%s", want, notes)
				}
				if log := h.mainLog(); strings.Contains(log, "A: ") {
					t.Errorf("A must not merge:\n%s", log)
				}
				if n := checkRuns(t, dir, "A"); n != 1 {
					t.Errorf("A's check ran %d times, want once, before the hand-back", n)
				}
				if log := h.mem.log("wt/A"); !strings.Contains(log, "A: fix the check") {
					t.Errorf("wt/A should keep its worker's fix:\n%s", log)
				}
				if tc.branch != "" {
					if log := h.mem.log(tc.branch); !strings.Contains(log, "A: fix the check") {
						t.Errorf("%s should be left as its worker left it:\n%s", tc.branch, log)
					}
				}
				if got := o.asideList(); !equal(got, []string{"A"}) {
					t.Errorf("set aside %v, want A", got)
				}
			})
		})
	}
}

// The rebase resolved with its conflict markers committed fails the check, but the worker then
// detaches its worktree's HEAD on the commit before, Base's, where the check passes.
func TestAResolutionLeftOffItsBranchIsRejected(t *testing.T) {
	t.Parallel()
	h := conflictHarness(t)
	h.beads.add("A", "first", 1)
	h.worker("A", conflicting(h.repo), func(w *fakeWorker) AgentState {
		resolvesWith("<<<<<<< ours\nmain\n=======\nA\n>>>>>>> theirs\n")(w)
		w.gitIn(w.wt, "checkout", "-q", "--detach", "HEAD~1")
		return "idle"
	})
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	wt := h.worktree("A")
	want := "handed back to its worker, but its worker left " + wt + " with a detached HEAD, not wt/A; wt/A is left at "
	if ev := h.sink.text(); !strings.Contains(ev, "MERGE_CONFLICT: A closed") || !strings.Contains(ev, want) ||
		!strings.Contains(ev, "before the rebase), as "+wt+" has a detached HEAD") {
		t.Errorf("events lack %q:\n%s", want, ev)
	}
	if got := read(t, filepath.Join(h.repo, "shared.txt")); got != "main\n" {
		t.Errorf("main must not change: shared.txt = %q", got)
	}
	if log := h.mainLog(); strings.Contains(log, "A: ") {
		t.Errorf("A must not merge:\n%s", log)
	}
}

// On a stub git: the verifiers reject a worktree off its branch, and restoreBranch resets nothing then,
// as a reset in the worktree would reset what it has checked out.
func TestAWorktreeOffItsBranchIsRejectedAndNotReset(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ on, says string }{
		{"other", "other checked out"},
		{"HEAD", "a detached HEAD"},
	} {
		why := "its worker left /wt/k-1 with " + tc.says + ", not wt/k-1"
		g := stubGit{on: tc.on, naming: "c1", commits: 1}
		if got := stubLoop(t, &g).verifyResolved(context.Background(), stubStop); got != why {
			t.Errorf("verifyResolved on %s = %q, want %q", tc.on, got, why)
		}
		if fixed, got := stubLoop(t, &g).verifyFixed(context.Background(), stubStop, attemptHead); fixed != "" ||
			got != why {
			t.Errorf("verifyFixed on %s = %q, %q, want \"\", %q", tc.on, fixed, got, why)
		}
		g = stubGit{on: tc.on, head: attemptHead}
		x := stubLoop(t, &g).restoreBranch(context.Background(), stubStop, beforeHead)
		if want := restoredAs(restored{now: attemptHead, off: tc.says}); x != want || g.resetTo != "" {
			t.Errorf("restoreBranch on %s = %+v, reset to %q; want %+v, no reset", tc.on, x, g.resetTo, want)
		}
		if got, want := x.afterFix(), "; wt/k-1 is left at 2222222222, as /wt/k-1 has "+tc.says; got != want {
			t.Errorf("afterFix = %q, want %q", got, want)
		}
		if got, want := x.afterResolution(), "wt/k-1 is left at 2222222222 (it was at 1111111111 before the rebase), "+
			"as /wt/k-1 has "+tc.says; got != want {
			t.Errorf("afterResolution = %q, want %q", got, want)
		}
	}
	// Back at its commit already, the branch needs no reset, and the worktree no look.
	g := stubGit{on: "other", head: beforeHead}
	if x := stubLoop(t, &g).restoreBranch(context.Background(), stubStop, beforeHead); x.off != "" || g.resetTo != "" {
		t.Errorf("restoreBranch = %+v, reset to %q; want it left as it is", x, g.resetTo)
	}
}
