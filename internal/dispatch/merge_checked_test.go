package dispatch

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
)

// orchestra-xj6z.7: the merge acts on what finish and the check looked at, not on what the worktree or
// the branch has come to since. finish looks at the worktree before the merge queue's wait, which can
// take many minutes, and a worker doesn't take the repository's lock to commit.

// changesBeforeTheMerge is git that runs change once, as the merge first asks whether the ticket's
// branch changes anything: what the maintainer or the ticket's worker did during the queue's wait.
type changesBeforeTheMerge struct {
	*fakeGit
	once   sync.Once
	change func()
}

func (g *changesBeforeTheMerge) Unchanged(ctx context.Context, repo, base, branch string) (bool, error) {
	g.once.Do(g.change)
	return g.fakeGit.Unchanged(ctx, repo, base, branch)
}

// commitsAnEmptyChange claims the ticket, commits nothing in a commit naming it and closes it: its branch's
// commits, taken together, change nothing, so the merge resets it to Base to drop it.
func commitsAnEmptyChange(w *fakeWorker) AgentState {
	w.claim()
	if err := w.git.commitIn(w.wt, w.id+": nothing to change"); err != nil {
		w.t.Error(err)
	}
	w.close()
	return "idle"
}

func TestAnUnchangedBranchIsNotResetOverWorkBegunSince(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, kind, want, on string // on: the branch the worktree is on, which the reset would move to main
		change               func(g *fakeGit, wt string)
	}{
		{"uncommitted edits", "CLOSED_UNCOMMITTED", "but %s has uncommitted changes", "wt/A",
			func(g *fakeGit, wt string) { g.leaveChange(wt, "new.go") }},
		{"another branch", "OFF_BRANCH", "but its worktree %s has scratch checked out, not wt/A", "scratch",
			func(g *fakeGit, wt string) { g.switchTo(wt, "scratch") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.beads.add("A", "first", 1)
				h.worker("A", commitsAnEmptyChange)
				o := h.loop()
				o.merger = &changesBeforeTheMerge{fakeGit: h.mem, change: func() { tc.change(h.mem, h.worktree("A")) }}
				o.Run(context.Background())
				wt := h.worktree("A")
				if want := strings.ReplaceAll(tc.want, "%s", wt); !h.sink.warnedWith(tc.kind, "A", want) {
					t.Errorf("events lack %s: %q:\n%s", tc.kind, want, h.sink.text())
				}
				if !exists(wt) {
					t.Error("A's worktree should be left for review")
				}
				if log := h.mem.log(tc.on); !strings.Contains(log, "A: nothing to change") {
					t.Errorf("%s should be left where it was, not reset to main:\n%s", tc.on, log)
				}
				if got := o.asideList(); !equal(got, []string{"A"}) {
					t.Errorf("set aside %v, want A", got)
				}
			})
		})
	}
}

// commitsLate is git whose fast-forward of the base finds a commit made on branch after it was
// checked, as its worker may make one without the repository's lock.
type commitsLate struct {
	*fakeGit
	branch string
}

func (g commitsLate) FastForward(ctx context.Context, repo, rev string) (string, error) {
	g.commit(g.branch, "A: made after the check", "late.txt")
	return g.fakeGit.FastForward(ctx, repo, rev)
}

func TestACommitMadeAfterTheCheckStaysOffBase(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Check = "true"
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		o := h.loop()
		o.merger = commitsLate{fakeGit: h.mem, branch: "wt/A"}
		o.Run(context.Background())
		main := h.mem.log("main")
		if !strings.Contains(main, "A: add a.txt") {
			t.Errorf("A's checked commit should merge:\n%s", main)
		}
		if strings.Contains(main, "made after the check") {
			t.Errorf("the commit made after the check must not reach main:\n%s", main)
		}
		if log := h.mem.log("wt/A"); !strings.Contains(log, "A: made after the check") {
			t.Errorf("wt/A should be kept with the commit made after the check:\n%s", log)
		}
		if ev := h.sink.text(); !strings.Contains(ev, "CLEANUP_FAILED for "+h.worktree("A")+" / wt/A") {
			t.Errorf("events should say wt/A was not cleaned up:\n%s", ev)
		}
	})
}
