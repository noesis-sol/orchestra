package dispatch

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
)

// What the loop asks git once where it used to ask more: the main checkout's state and branch from one
// git status; nothing about an earlier attempt on a branch cut from Base just now; and a finished
// branch's commits, the one naming its ticket and how many, from one git log.

// askedGit is git in memory, recording what the loop asks it of a ticket's branch and worktree.
type askedGit struct {
	*fakeGit
	mu    sync.Mutex
	asked []string
}

func (g *askedGit) ask(what string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.asked = append(g.asked, what)
}

func (g *askedGit) IsAncestor(ctx context.Context, repo, ancestor, rev string) bool {
	g.ask("IsAncestor " + ancestor + " " + rev)
	return g.fakeGit.IsAncestor(ctx, repo, ancestor, rev)
}

func (g *askedGit) CountCommits(ctx context.Context, repo, revs string) int {
	g.ask("CountCommits " + revs)
	return g.fakeGit.CountCommits(ctx, repo, revs)
}

func (g *askedGit) CommitNaming(ctx context.Context, repo, base, branch, ticket string) string {
	g.ask("CommitNaming " + base + ".." + branch)
	return g.fakeGit.CommitNaming(ctx, repo, base, branch, ticket)
}

func (g *askedGit) OwnCommits(ctx context.Context, repo, base, branch, ticket string) (string, int) {
	g.ask("OwnCommits " + base + ".." + branch)
	return g.fakeGit.OwnCommits(ctx, repo, base, branch, ticket)
}

func (g *askedGit) Head(ctx context.Context, repo, rev string) string {
	g.ask("Head " + rev)
	return g.fakeGit.Head(ctx, repo, rev)
}

func (g *askedGit) DirtyWorktree(ctx context.Context, dir string) string {
	g.ask("DirtyWorktree " + filepath.Base(dir))
	return g.fakeGit.DirtyWorktree(ctx, dir)
}

func (g *askedGit) DirtyTreeAndBranch(ctx context.Context, dir string) (string, string, error) {
	g.ask("DirtyTreeAndBranch")
	return g.fakeGit.DirtyTreeAndBranch(ctx, dir)
}

// A new ticket's branch, cut from Base as its worktree is made, is on top of Base with nothing of an
// earlier attempt: git is asked nothing of it until its worker finishes. Then one git log gives the
// commit naming the ticket and how many it has, which the fast-forward names as it merges it.
func TestANewTicketsBranchIsAskedAboutOnceItFinishes(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		g := &askedGit{fakeGit: h.mem}
		o := h.loop()
		o.checkout, o.worktrees, o.merger = g, g, g
		if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		// The main checkout before A starts, as A goes to merge, before its fast-forward, and once
		// it has merged, as the loop looks for the next ticket; the loop and the merge ask side by side.
		// Base's head as the run starts and before the fast-forward: after it, Base is at the branch's
		// head, which the merge read as it found the branch on top of Base.
		want := []string{
			"Head main", "DirtyTreeAndBranch", "Head wt/A",
			"OwnCommits main..wt/A", "DirtyWorktree A",
			"DirtyTreeAndBranch",
			"IsAncestor main wt/A", "Head wt/A",
			"DirtyTreeAndBranch", "Head main",
			"DirtyTreeAndBranch",
		}
		if got := slices.Sorted(slices.Values(g.asked)); !slices.Equal(got, slices.Sorted(slices.Values(want))) {
			t.Errorf("git was asked:\n%q\nwant, in any order:\n%q", g.asked, want)
		}
	})
}

// The main checkout's branch, as git status names it, is Base as 'git symbolic-ref --short' named it
// when the run started: with heads/ before it where a tag has the branch's name.
func TestIsBranch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		branch, base string
		want         bool
	}{
		{"main", "main", true},
		{"main", "heads/main", true},
		{"main", "refs/heads/main", true},
		{"heads/main", "heads/main", true}, // a branch named heads/main
		{"main", "other", false},
		{"other", "heads/main", false},
		{"", "main", false}, // detached
		{"main", "tags/main", false},
	} {
		if got := isBranch(tc.branch, tc.base); got != tc.want {
			t.Errorf("isBranch(%q, %q) = %v, want %v", tc.branch, tc.base, got, tc.want)
		}
	}
}
