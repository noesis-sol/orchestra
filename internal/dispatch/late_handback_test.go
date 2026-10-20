package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// orchestra-nr1v.1: a hand-back past its time limit with its worker still at it. tutti-nrg.5.9's worker
// had committed its resolution 9 minutes before the limit, and was still running its tests: the branch
// was put back under it, the resolution dropped, and nothing told it, so it worked on for an hour on a
// ticket set aside. What git has is looked at first now, and the worker is stopped and told either way.

// idles is a worker that, given a message, takes it and goes idle.
func idles(w *fakeWorker) AgentState { return "idle" }

// stillWorking is a worker that does as b does and goes on working after, as one running its tests.
func stillWorking(b behaviour) behaviour {
	return func(w *fakeWorker) AgentState {
		b(w)
		return "working"
	}
}

// replayed has the rebase stopped in worktree wt get as far as a commit on top of onto with subject and
// files, as a worker resolving its first commit and continuing would, and returns the commit's hash.
func (g *fakeGit) replayed(wt, onto, subject string, files ...string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.replaying == nil {
		g.replaying = map[string][]fakeCommit{}
	}
	c := g.newCommit(subject, files...)
	g.replaying[wt] = append(g.resolve(onto), c)
	g.replaced = append(g.replaced, g.replaying[wt]) // git keeps it once the rebase is aborted
	return c.hash
}

// lateHarness is a timed run whose ticket A closes with a commit that conflicts with main, its rebase
// handed back to its worker, whose behaviours from there the test gives.
func lateHarness(t *testing.T) *harness {
	t.Helper()
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
	})
	return h
}

// stoppedThenTold checks that ticket id's worker was handed its ticket back, then stopped with Escape
// and given a message, the last thing to reach it, and returns that message.
func stoppedThenTold(t *testing.T, h *harness, id string) string {
	t.Helper()
	typed := h.herdr.typedTo(id)
	if n := len(typed); n < 3 || typed[n-2] != "esc" || typed[n-1] != "prompt" {
		t.Fatalf("typed to %s's worker: %v, want Escape and a message last", id, typed)
	}
	texts := h.herdr.pastedText(id)
	return texts[len(texts)-1]
}

// A worker whose rebase has finished, with the branch on top of main, as the limit passes, its tests
// still running, is stopped and told its branch is taken as it is, and the ticket merges.
func TestAResolutionCommittedBeforeTheLimitMergesAfterIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := lateHarness(t)
		h.worker("A", stillWorking(resolvesInMemory(h.mem)), idles)
		var prompted []time.Time
		h.herdr.onPrompt = func(id string) { prompted = append(prompted, time.Now()) }
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		if h.sink.warned("MERGE_CONFLICT", "") || !h.sink.has(EvClosed, "A") ||
			!strings.Contains(ev, "A's worker was still at its hand-back after 20m, but its rebase has finished: "+
				"wt/A is taken as it is") {
			t.Errorf("events:\n%s", ev)
		}
		if log := h.mainLog(); !strings.Contains(log, "A: add shared.txt") {
			t.Errorf("A should merge into main:\n%s", log)
		}
		if len(prompted) != 2 {
			t.Fatalf("%d prompts, want the hand-back and the word that it is over\n%s", len(prompted), ev)
		}
		if took := prompted[1].Sub(prompted[0]) - escapeSettle; took < project.DefaultResolveTimeout ||
			took > project.DefaultResolveTimeout+2*statusPoll {
			t.Errorf("told %s after the hand-back, want %s", took, project.DefaultResolveTimeout)
		}
		told := stoppedThenTold(t, h, "A")
		for _, want := range []string{"the 20m for this hand-back is up, and your rebase has finished",
			"takes wt/A as it is now", "to check with 'true' and merge", "Leave " + h.worktree("A") + " as it is"} {
			if !strings.Contains(told, want) {
				t.Errorf("told %q, want %q in it", told, want)
			}
		}
	})
}

// A worker still mid-rebase as the limit passes is stopped and told the hand-back is over, its attempt
// named, before its rebase is aborted and the ticket set aside.
func TestAWorkerMidRebaseAtTheLimitIsStoppedAndToldFirst(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		var attempt string
		h := lateHarness(t)
		wt := h.worktree("A")
		h.worker("A", func(w *fakeWorker) AgentState {
			attempt = w.git.replayed(wt, "main", "A: add shared.txt", "shared.txt")
			return "working" // on the next commit of the rebase
		}, idles)
		var before string // where wt/A was before the rebase
		var midRebase []bool
		h.herdr.onPrompt = func(id string) {
			if before == "" {
				before = h.mem.head("wt/A")
			}
			midRebase = append(midRebase, h.mem.inRebase(wt))
		}
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		if !h.sink.warnedWith("MERGE_CONFLICT", "A", "handed back to its worker, but its worker was still working "+
			"after 20m; the rebase was aborted") || !strings.Contains(ev, "A's worker was stopped and told its hand-back "+
			"is over") {
			t.Errorf("events:\n%s", ev)
		}
		told := stoppedThenTold(t, h, "A")
		for _, want := range []string{"the 20m for this hand-back is up, so orchestra has stopped you and ends it",
			"aborts the rebase if it is still in progress, puts wt/A back at " + short(before) +
				", where it was before the rebase,", "sets A aside for review",
			"Your attempt so far is commit " + attempt + ", which git keeps (git show " + attempt + ")",
			"no git commands in " + wt} {
			if !strings.Contains(told, want) {
				t.Errorf("told %q, want %q in it", told, want)
			}
		}
		if len(midRebase) != 2 || !midRebase[1] {
			t.Errorf("mid-rebase as each prompt was given: %v, want the last told before the rebase is aborted",
				midRebase)
		}
		if h.mem.inRebase(wt) || h.mem.head("wt/A") != before {
			t.Errorf("wt/A should be put back at %s, its rebase aborted: at %s", before, h.mem.head("wt/A"))
		}
		if n := h.beads.notesOf("A"); !strings.Contains(n, "its worker was asked to resolve the rebase, but its "+
			"worker was still working after 20m") {
			t.Errorf("notes:\n%s", n)
		}
		if a, _ := h.beads.Show(context.Background(), "A"); !HasLabel(a, UnmergedLabel) {
			t.Errorf("A should be labelled %q: %v", UnmergedLabel, a.Labels)
		}
	})
}

// A fix committed before the limit, its worker still at it, is checked and merges; one still not
// committed at the limit ends the hand-back, its worker told the branch is put back where the check
// failed.
func TestAFixHandBackAtTheLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		fix       func(dir string) behaviour
		merged    bool
		told, log string
	}{
		{"committed", func(dir string) behaviour { return stillWorking(commitsFix(dir, "fix.txt", true)) }, true,
			"the 20m for this hand-back is up, and your fix is committed",
			"A's worker was still at its hand-back after 20m, but its fix is committed: wt/A is taken as it is"},
		{"not committed", func(string) behaviour { return func(*fakeWorker) AgentState { return "working" } }, false,
			"where the check failed, and sets A aside for review. Nothing of your attempt was committed",
			"A's worker was stopped and told its hand-back is over"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				dir := t.TempDir()
				h := checkHandBackHarness(t, dir, tc.fix(dir), idles)
				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				ev := h.sink.text()
				if h.sink.has(EvClosed, "A") != tc.merged || h.sink.warned("CHECKS_FAILED", "A") == tc.merged ||
					!strings.Contains(ev, tc.log) {
					t.Errorf("events:\n%s", ev)
				}
				if tc.merged && !strings.Contains(ev, "passes on wt/A as its worker fixed it") {
					t.Errorf("events:\n%s", ev)
				}
				if n := h.beads.notesOf("A"); !tc.merged && !strings.Contains(n, "its worker was still working after 20m") {
					t.Errorf("notes:\n%s", n)
				}
				if told := stoppedThenTold(t, h, "A"); !strings.Contains(told, tc.told) {
					t.Errorf("told %q, want %q in it", told, tc.told)
				}
			})
		})
	}
}
