package dispatch

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
)

// A git call on the merge path that fails to answer, as one that times out on a loaded machine, isn't
// taken for an answer: not for "no commit names the ticket", "not on top of Base" or "changes nothing",
// which would set the ticket aside, rebase it or close it as unchanged. It is asked again, and a
// failure that lasts stops the run with GIT_FAILED, the ticket left unmerged and its branch as its
// worker left it.

// errGitTimedOut is git failing to answer: not an answer of no.
var errGitTimedOut = errors.New("git: timed out after 30s")

// mergeGitFails is git in memory whose answer to one question about wt/A (fail, the method's name)
// fails, the next times calls, or every call when times is negative.
type mergeGitFails struct {
	*fakeGit
	fail  string
	mu    sync.Mutex
	times int
	asked int // how many times the failing question was asked
}

// failsNow reports whether method's answer about rev (a branch, revs or a worktree) fails this time.
func (g *mergeGitFails) failsNow(method, rev string) bool {
	if method != g.fail || !strings.Contains(rev, "wt/A") && !strings.HasSuffix(rev, "/A") {
		return false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.asked++
	if g.times == 0 {
		return false
	}
	g.times--
	return true
}

func (g *mergeGitFails) OwnCommits(ctx context.Context, repo, base, branch, ticket string) (string, int, error) {
	if g.failsNow("OwnCommits", branch) {
		return "", 0, errGitTimedOut
	}
	return g.fakeGit.OwnCommits(ctx, repo, base, branch, ticket)
}

func (g *mergeGitFails) DirtyWorktree(ctx context.Context, dir string) (string, error) {
	if g.failsNow("DirtyWorktree", dir) {
		return "", errGitTimedOut
	}
	return g.fakeGit.DirtyWorktree(ctx, dir)
}

func (g *mergeGitFails) Changes(ctx context.Context, repo string, paths ...string) (map[string]string, error) {
	if g.failsNow("Changes", repo) {
		return nil, errGitTimedOut
	}
	return g.fakeGit.Changes(ctx, repo, paths...)
}

func (g *mergeGitFails) Unchanged(ctx context.Context, repo, base, branch string) (bool, error) {
	if g.failsNow("Unchanged", branch) {
		return false, errGitTimedOut
	}
	return g.fakeGit.Unchanged(ctx, repo, base, branch)
}

func (g *mergeGitFails) IsAncestor(ctx context.Context, repo, ancestor, rev string) (bool, error) {
	if g.failsNow("IsAncestor", rev) {
		return false, errGitTimedOut
	}
	return g.fakeGit.IsAncestor(ctx, repo, ancestor, rev)
}

func (g *mergeGitFails) Head(ctx context.Context, repo, rev string) (string, error) {
	if g.failsNow("Head", rev) {
		return "", errGitTimedOut
	}
	return g.fakeGit.Head(ctx, repo, rev)
}

func (g *mergeGitFails) CountCommits(ctx context.Context, repo, revs string) (int, error) {
	if g.failsNow("CountCommits", revs) {
		return 0, errGitTimedOut
	}
	return g.fakeGit.CountCommits(ctx, repo, revs)
}

// mergeGitFailsRun runs A, which commits a.txt and closes, on git whose answer to fail about A fails
// times times (every time when negative); with moved, a commit lands on main while A runs, so its
// merge rebases it. It returns the harness, the loop and the git.
func mergeGitFailsRun(t *testing.T, fail string, times int, moved bool) (*harness, *Loop, *mergeGitFails) {
	t.Helper()
	h := newTimedHarness(t)
	h.beads.add("A", "first", 1)
	h.worker("A", func(w *fakeWorker) AgentState {
		w.claim()
		w.commit("a.txt")
		if moved {
			w.git.commit("main", "landed on main: landed.txt", "landed.txt")
		}
		w.close()
		return "idle"
	})
	g := &mergeGitFails{fakeGit: h.mem, fail: fail, times: times}
	o := h.loop()
	o.checkout, o.merger = g, g
	o.Run(t.Context())
	return h, o, g
}

// Whichever question the merge turns on git can't answer, A isn't merged, set aside as if it had no
// commit, closed as unchanged or rebased on a guess: the run stops with GIT_FAILED and git's error,
// A left unmerged and labelled, its branch as its worker left it.
func TestAMergeGitCantAnswerStopsTheRun(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fail  string
		moved bool // main moved on while A ran: the merge rebases it
		what  string
	}{
		{"OwnCommits", false, "which commits wt/A has beyond main"},
		{"DirtyWorktree", false, "has uncommitted changes"},
		{"Unchanged", false, "whether the commits on wt/A change anything"},
		{"IsAncestor", false, "whether wt/A is on top of main"},
		{"Head", false, "where wt/A is"},
		{"Changes", true, "has uncommitted changes"},
		{"CountCommits", true, "how many commits wt/A has beyond main"},
	} {
		t.Run(tc.fail, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h, o, g := mergeGitFailsRun(t, tc.fail, -1, tc.moved)
				if !strings.HasPrefix(o.Final(), "GIT_FAILED: git can't tell ") || !strings.Contains(o.Final(), tc.what) ||
					!strings.Contains(o.Final(), errGitTimedOut.Error()) {
					t.Fatalf("final %q, want GIT_FAILED saying git can't tell %s, with git's error\n%s",
						o.Final(), tc.what, h.sink.text())
				}
				if main := h.mem.log("main"); strings.Contains(main, "A: add a.txt") {
					t.Errorf("A merged though git couldn't tell %s: %s", tc.what, main)
				}
				if br := h.mem.log("wt/A"); tc.moved && strings.Contains(br, "landed") {
					t.Errorf("wt/A was rebased though git couldn't tell %s: %s", tc.what, br)
				}
				ev := h.sink.text()
				for _, wrong := range []string{"CLOSED_WITHOUT_COMMIT", "CLOSED_UNCOMMITTED", "no change to merge",
					"change nothing"} {
					if strings.Contains(ev, wrong) {
						t.Errorf("a failed %s read as an answer (%s):\n%s", tc.fail, wrong, ev)
					}
				}
				if a, _ := h.beads.Show(t.Context(), "A"); !HasLabel(a, UnmergedLabel) {
					t.Errorf("A, left unmerged, should be labelled %q: %v", UnmergedLabel, a.Labels)
				}
				if g.asked < gitTries {
					t.Errorf("git was asked %d times, want %d before the run stops", g.asked, gitTries)
				}
			})
		})
	}
}

// A question git fails to answer once, then answers, holds nothing up: A merges.
func TestAMergeGitFailsToAnswerOnceMerges(t *testing.T) {
	t.Parallel()
	for _, fail := range []string{"OwnCommits", "Unchanged", "IsAncestor", "CountCommits"} {
		t.Run(fail, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h, o, _ := mergeGitFailsRun(t, fail, 1, fail == "CountCommits")
				if code := o.Final(); !strings.HasPrefix(code, "READY_EMPTY") {
					t.Fatalf("final %q, want A merged and the run to end as nothing is ready\n%s", code, h.sink.text())
				}
				if main := h.mem.log("main"); !strings.Contains(main, "A: add a.txt") {
					t.Errorf("A didn't merge once git answered: %s\n%s", main, h.sink.text())
				}
			})
		})
	}
}
