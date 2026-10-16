package dispatch

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// Putting a ticket's branch back after a hand-back to its worker is rejected, and checking what the
// worker left, on a stub git.

// stubGit is git as a hand-back's worker left the ticket's worktree: whether a rebase is in progress and
// can be aborted, the worktree's changes and what it has checked out, the branch's commit, and the
// answers to the checks on it. It records the reset.
type stubGit struct {
	upToDate
	cleanCheckout
	rebasing     bool
	abortErr     error
	dirty        string
	on           string // what the worktree has checked out, when not the ticket's branch ("HEAD": detached)
	head         string
	resetErr     error
	resetTo      string
	notAncestor  bool
	unchanged    bool
	naming       string
	commits      int
	notNaming    string
	notNamingErr error
}

func (g *stubGit) RebaseInProgress(ctx context.Context, worktree string) bool { return g.rebasing }
func (g *stubGit) AbortRebase(ctx context.Context, worktree string) (string, error) {
	if g.abortErr == nil {
		g.rebasing = false
	}
	return "", g.abortErr
}
func (g *stubGit) DirtyWorktree(ctx context.Context, dir string) string { return g.dirty }
func (g *stubGit) Head(ctx context.Context, repo, rev string) string    { return g.head }
func (g *stubGit) ResetBranch(ctx context.Context, worktree, rev string) (string, error) {
	g.resetTo = rev
	return "", g.resetErr
}
func (g *stubGit) CurrentBranch(ctx context.Context, repo string) (string, error) {
	switch g.on {
	case "":
		return stubStop.br, nil
	case "HEAD":
		return "", nil
	}
	return g.on, nil
}
func (g *stubGit) IsAncestor(ctx context.Context, repo, ancestor, rev string) bool {
	return !g.notAncestor
}
func (g *stubGit) Unchanged(ctx context.Context, repo, base, branch string) bool { return g.unchanged }
func (g *stubGit) CommitNaming(ctx context.Context, repo, base, branch, ticket string) string {
	return g.naming
}
func (g *stubGit) CountCommits(ctx context.Context, repo, revs string) int { return g.commits }
func (g *stubGit) CommitNotNaming(ctx context.Context, repo, revs, ticket string) (string, error) {
	return g.notNaming, g.notNamingErr
}

// stubLoop is a loop on g, logging to a file of the test's own.
func stubLoop(t *testing.T, g *stubGit) *Loop {
	t.Helper()
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = log.Close() }) // a log the test doesn't read
	return New(Config{Repo: "/repo", Base: "main"}, log, "", Deps{Checkout: g, Merger: g})
}

const (
	beforeHead  = "1111111111aaaa" // the commit a branch is put back at
	attemptHead = "2222222222bbbb" // where the worker left it
)

var stubStop = rebaseStop{worker: worker{id: "k-1", br: "wt/k-1", wt: "/wt/k-1"}, onto: "main1", head: beforeHead,
	own: 2}

func TestRestoreBranch(t *testing.T) {
	failed := errors.New("exit status 128")
	for _, tc := range []struct {
		name    string
		git     stubGit
		want    restored
		resetTo string
	}{
		{"as it was", stubGit{head: beforeHead}, restored{now: beforeHead}, ""},
		{"rebase aborted", stubGit{rebasing: true, head: beforeHead}, restored{aborted: true, now: beforeHead}, ""},
		{"rebase stuck", stubGit{rebasing: true, abortErr: failed, head: attemptHead}, restored{stuck: true}, ""},
		{"reset", stubGit{head: attemptHead}, restored{now: attemptHead}, beforeHead},
		{"reset after an abort", stubGit{rebasing: true, head: attemptHead},
			restored{aborted: true, now: attemptHead}, beforeHead},
		{"reset fails", stubGit{head: attemptHead, resetErr: failed}, restored{now: attemptHead, err: failed}, beforeHead},
		{"dirty", stubGit{head: attemptHead, dirty: " M a.go"}, restored{dirty: true, now: attemptHead}, ""},
		{"dirty as it was", stubGit{head: beforeHead, dirty: " M a.go"}, restored{dirty: true, now: beforeHead}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := tc.git
			got := stubLoop(t, &g).restoreBranch(context.Background(), stubStop, beforeHead)
			tc.want.br, tc.want.wt, tc.want.to = stubStop.br, stubStop.wt, beforeHead
			if got != tc.want {
				t.Errorf("restoreBranch = %+v, want %+v", got, tc.want)
			}
			if g.resetTo != tc.resetTo {
				t.Errorf("reset to %q, want %q", g.resetTo, tc.resetTo)
			}
		})
	}
}

// restoredAs is what restoreBranch says it did with stubStop's branch, put back at beforeHead.
func restoredAs(x restored) restored {
	x.br, x.wt, x.to = stubStop.br, stubStop.wt, beforeHead
	return x
}

func TestAfterResolution(t *testing.T) {
	failed := errors.New("exit status 128")
	for _, tc := range []struct {
		x    restored
		want string
	}{
		{restored{stuck: true}, "the rebase could not be aborted, so /wt/k-1 is left mid-rebase"},
		{restored{aborted: true, now: beforeHead}, "the rebase was aborted"},
		{restored{aborted: true, dirty: true, now: beforeHead}, "the rebase was aborted"},
		{restored{now: beforeHead}, "wt/k-1 is as the ticket closed it"},
		{restored{dirty: true, now: beforeHead}, "wt/k-1 is as the ticket closed it"},
		{restored{dirty: true, now: attemptHead},
			"wt/k-1 is left as its worker left it (it was at 1111111111 before the rebase)"},
		{restored{now: attemptHead, err: failed},
			"wt/k-1 could not be reset to 1111111111, where it was before the rebase; it is left at 2222222222"},
		{restored{now: attemptHead},
			"wt/k-1 was reset to 1111111111, where it was before the rebase (its worker's attempt is 2222222222)"},
		{restored{aborted: true, now: attemptHead},
			"wt/k-1 was reset to 1111111111, where it was before the rebase (its worker's attempt is 2222222222)"},
	} {
		if got := restoredAs(tc.x).afterResolution(); got != tc.want {
			t.Errorf("afterResolution(%+v) = %q, want %q", tc.x, got, tc.want)
		}
	}
}

func TestAfterFix(t *testing.T) {
	failed := errors.New("exit status 128")
	for _, tc := range []struct {
		x    restored
		want string
	}{
		{restored{stuck: true}, "; its rebase could not be aborted, so /wt/k-1 is left mid-rebase"},
		{restored{dirty: true, now: beforeHead}, "; /wt/k-1 is left as its worker left it"},
		{restored{dirty: true, now: attemptHead}, "; /wt/k-1 is left as its worker left it"},
		{restored{now: beforeHead}, ""},
		{restored{aborted: true, now: beforeHead}, ""},
		{restored{now: attemptHead, err: failed},
			"; wt/k-1 could not be reset to 1111111111, where the check failed, and is left at 2222222222"},
		{restored{now: attemptHead},
			"; wt/k-1 was reset to 1111111111, where the check failed (its worker's attempt is 2222222222)"},
	} {
		if got := restoredAs(tc.x).afterFix(); got != tc.want {
			t.Errorf("afterFix(%+v) = %q, want %q", tc.x, got, tc.want)
		}
	}
}

func TestVerifyResolved(t *testing.T) {
	for _, tc := range []struct {
		git  stubGit
		want string
	}{
		{stubGit{naming: "c1", commits: 2}, ""},
		{stubGit{naming: "c1", commits: 1}, ""}, // a commit skipped
		{stubGit{unchanged: true}, ""},          // every commit skipped: closed as unchanged
		{stubGit{rebasing: true}, "its worker left the rebase unfinished"},
		{stubGit{dirty: " M a.go"}, "its worker left uncommitted changes in /wt/k-1"},
		{stubGit{notAncestor: true}, "wt/k-1 is not on top of main as it was rebased onto"},
		{stubGit{commits: 2}, "no commit on wt/k-1 names k-1 any more"},
		{stubGit{naming: "c1", commits: 3},
			"wt/k-1 has 3 commits where the ticket had 2 (a commit made besides the rebase?)"},
		{stubGit{naming: "c1", commits: -1},
			"wt/k-1 has -1 commits where the ticket had 2 (a commit made besides the rebase?)"},
	} {
		g := tc.git
		if got := stubLoop(t, &g).verifyResolved(context.Background(), stubStop); got != tc.want {
			t.Errorf("verifyResolved on %+v = %q, want %q", tc.git, got, tc.want)
		}
	}
}

func TestVerifyFixed(t *testing.T) {
	for _, tc := range []struct {
		git        stubGit
		fixed, why string
	}{
		{stubGit{commits: 1}, "1 commit", ""},
		{stubGit{commits: 2}, "2 commits", ""},
		{stubGit{rebasing: true}, "", "its worker left a rebase in progress in /wt/k-1"},
		{stubGit{dirty: " M a.go"}, "", "its worker left uncommitted changes in /wt/k-1"},
		{stubGit{notAncestor: true}, "", "its worker rewrote the commits on wt/k-1 rather than add to them"},
		{stubGit{commits: -1}, "", "git could not say what its worker committed"},
		{stubGit{commits: 1, notNamingErr: errors.New("exit status 128")}, "",
			"git could not say what its worker committed: exit status 128"},
		{stubGit{}, "", "its worker committed nothing"},
		{stubGit{commits: 2, notNaming: "abc123 fix the test"}, "",
			"its worker's commit abc123 fix the test does not name k-1"},
	} {
		g := tc.git
		fixed, why := stubLoop(t, &g).verifyFixed(context.Background(), stubStop, attemptHead)
		if fixed != tc.fixed || why != tc.why {
			t.Errorf("verifyFixed on %+v = %q, %q, want %q, %q", tc.git, fixed, why, tc.fixed, tc.why)
		}
	}
}
