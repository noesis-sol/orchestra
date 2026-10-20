package dispatch

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// What a hand-back's worker left is checked without taking a git call that fails for an answer: a
// rebase git can't tell is over, uncommitted changes it can't rule out, or commits it can't list
// reject it with git's error, and its branch is put back only where git says where it is.

// errStubGit is stubGit's error for the method failing.
var errStubGit = errors.New("git: timed out after 30s")

func TestVerifyResolvedRejectsWhatGitCantTell(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ fail, why string }{
		{"RebaseInProgress", "git could not say whether its worker left a rebase in progress in /wt/k-1"},
		{"DirtyWorktree", "git could not say whether its worker left uncommitted changes in /wt/k-1"},
		{"Unchanged", "git could not say whether the commits on wt/k-1 change anything"},
		{"OwnCommits", "git could not say what its worker left on wt/k-1"},
	} {
		g := stubGit{naming: "c1", commits: 2, failing: tc.fail}
		want := tc.why + ": " + errStubGit.Error()
		if got := stubLoop(t, &g).verifyResolved(context.Background(), stubStop); got != want {
			t.Errorf("%s failing: verifyResolved = %q, want %q", tc.fail, got, want)
		}
	}
}

func TestVerifyFixedRejectsWhatGitCantTell(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ fail, why string }{
		{"RebaseInProgress", "git could not say whether its worker left a rebase in progress in /wt/k-1"},
		{"DirtyWorktree", "git could not say whether its worker left uncommitted changes in /wt/k-1"},
		{"CountCommits", "git could not say what its worker committed"},
	} {
		g := stubGit{commits: 1, failing: tc.fail}
		want := tc.why + ": " + errStubGit.Error()
		if fixed, why := stubLoop(t, &g).verifyFixed(context.Background(), stubStop, attemptHead); fixed != "" ||
			why != want {
			t.Errorf("%s failing: verifyFixed = %q, %q; want none, %q", tc.fail, fixed, why, want)
		}
	}
}

// A branch git can't tell the state of is left as its worker left it: nothing aborted or reset.
func TestRestoreBranchLeavesWhatGitCantTell(t *testing.T) {
	t.Parallel()
	for _, fail := range []string{"RebaseInProgress", "DirtyWorktree", "Head"} {
		g := stubGit{rebasing: fail != "RebaseInProgress", head: attemptHead, failing: fail}
		x := stubLoop(t, &g).restoreBranch(context.Background(), stubStop, beforeHead)
		if !errors.Is(x.unknown, errStubGit) || x.aborted && fail == "RebaseInProgress" || g.resetTo != "" {
			t.Errorf("%s failing: restoreBranch = %+v, reset to %q; want git's error, nothing reset", fail, x, g.resetTo)
		}
		if got := x.afterResolution(); !strings.Contains(got, "git could not say what /wt/k-1 has: "+errStubGit.Error()) ||
			!strings.Contains(got, "wt/k-1 is left as its worker left it") {
			t.Errorf("%s failing: afterResolution = %q", fail, got)
		}
		if got := x.afterFix(); got != "; git could not say what /wt/k-1 has: "+errStubGit.Error()+
			", so it is left as its worker left it" {
			t.Errorf("%s failing: afterFix = %q", fail, got)
		}
	}
}
