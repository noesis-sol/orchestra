package dispatch

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// A closed ticket whose branch git can't look up, as when git show-ref times out on a loaded
// machine, is not merged for that: the tickets it blocks wait until git can tell, and what git
// failed to say isn't kept. A branch git says isn't there, its exit 1, still counts as merged
// (TestClosedBlockerWithNothingOffBaseHoldsNothing).

// errShowRef is git show-ref failing to answer: not its exit 1, which says there is no such branch.
var errShowRef = errors.New("git show-ref --verify --quiet refs/heads/wt/A: timed out after 30s")

// gitFails is git in memory whose answers about wt/A fail while failing is set: HasBranch, or
// IsAncestor and Holds, as fail says. It notes the commit the base was on each time wt/A's existence
// is asked.
type gitFails struct {
	*fakeGit
	fail    string // the method that fails: "HasBranch", "IsAncestor" or "Holds"
	mu      sync.Mutex
	failing bool
	asked   []string
}

func (g *gitFails) failingNow(method, branch string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.failing && g.fail == method && branch == "wt/A"
}

func (g *gitFails) stop() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.failing = false
}

func (g *gitFails) heads() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.asked...)
}

func (g *gitFails) HasBranch(ctx context.Context, repo, branch string) (bool, error) {
	if branch == "wt/A" {
		head := g.head(g.base)
		g.mu.Lock()
		g.asked = append(g.asked, head)
		g.mu.Unlock()
	}
	if g.failingNow("HasBranch", branch) {
		return false, errShowRef
	}
	return g.fakeGit.HasBranch(ctx, repo, branch)
}

func (g *gitFails) IsAncestor(ctx context.Context, repo, ancestor, rev string) (bool, error) {
	if g.failingNow("IsAncestor", ancestor) {
		return false, errShowRef
	}
	return g.fakeGit.IsAncestor(ctx, repo, ancestor, rev)
}

func (g *gitFails) Holds(ctx context.Context, repo, base, branch string) (bool, error) {
	if g.failingNow("Holds", branch) {
		return false, errShowRef
	}
	return g.fakeGit.Holds(ctx, repo, base, branch)
}

// A, closed, has no branch: merged, once git can say so. While git show-ref fails, B, which A
// blocks, waits, saying why, with no CLOSED_UNMERGED warning; git is asked again with main on the
// same commit, and B starts once it answers, while C, beside it, still runs.
func TestClosedTicketGitCantLookUpHoldsItsDependents(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "closed by hand", 1)
		h.beads.set("A", StatusClosed)
		h.beads.add("C", "beside", 1)
		h.beads.add("B", "blocked by A", 2)
		h.beads.link("B", "A", "blocks")
		g := &gitFails{fakeGit: h.mem, fail: "HasBranch", failing: true}
		start := h.mem.head("main")
		bStarted := make(chan struct{})
		h.worker("C", func(w *fakeWorker) AgentState {
			time.Sleep(2 * time.Minute) // B is held meanwhile, poll after poll
			g.stop()
			select {
			case <-bStarted:
			case <-time.After(time.Hour):
				t.Error("B didn't start once git could tell A has no branch")
			}
			return finishes("c.txt")(w)
		})
		h.worker("B", func(w *fakeWorker) AgentState {
			close(bStarted)
			return finishes("b.txt")(w)
		})
		o := h.loop()
		o.worktrees = g
		if code := o.Run(t.Context()); code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.dispatched(); !equal(got, []string{"C", "B"}) {
			t.Errorf("dispatched %v, want C, then B once git could tell", got)
		}
		ev := h.sink.text()
		if want := "  B waits: A closed but not merged (git can't tell whether wt/A is on main: " +
			errShowRef.Error() + ")"; !strings.Contains(ev, want) {
			t.Errorf("events don't say %q:\n%s", want, ev)
		}
		if h.sink.warned("CLOSED_UNMERGED", "") {
			t.Errorf("A warned of as closed unmerged:\n%s", ev)
		}
		if n := strings.Count(strings.Join(g.heads(), " "), start); n < 3 {
			t.Errorf("git asked of wt/A %d times with main on %s (%v), want again after each failure",
				n, start, g.heads())
		}
	})
}

// Whichever of git's three questions fails, branchOff says git can't tell, with git's error, and
// closedBranches keeps nothing of it: it asks again, and warns of nothing, until git answers.
func TestClosedBranchGitFailingIsAskedAgain(t *testing.T) {
	t.Parallel()
	for _, fail := range []string{"HasBranch", "IsAncestor", "Holds"} {
		t.Run(fail, func(t *testing.T) {
			t.Parallel()
			c := Config{Repo: t.TempDir(), Base: "main"}
			g := &gitFails{fakeGit: newFakeGit(c.Base), fail: fail, failing: true}
			cutBranch(g.fakeGit, "wt/A")
			g.commit("wt/A", "A: add a.txt", "a.txt")
			ctx := context.Background()
			why, err := branchOff(ctx, c, g, g, "A")
			if want := "git can't tell whether wt/A is on main"; why != want || !errors.Is(err, errShowRef) {
				t.Errorf("branchOff = %q, %v; want %q, %v", why, err, want, errShowRef)
			}

			var b closedBranches
			asked := 0
			ask := func() (string, bool) {
				return b.why("A", "h1", func() string { return "t1" }, func() (string, error) {
					asked++
					return branchOff(ctx, c, g, g, "A")
				})
			}
			for i := range 2 {
				why, first := ask()
				if want := "git can't tell whether wt/A is on main: " + errShowRef.Error(); why != want || first {
					t.Errorf("ask %d: why = %q, first %v; want %q, not first", i, why, first, want)
				}
			}
			g.stop()
			for range 2 {
				if why, _ := ask(); why != "wt/A is not on main" {
					t.Errorf("once git answers, why = %q, want wt/A is not on main", why)
				}
			}
			if asked != 3 {
				t.Errorf("git asked %d times, want each time it failed and once more", asked)
			}
		})
	}
}

// The check for nothing to run counts a ticket blocked by a closed ticket git can't look up the
// branch of as waiting for it, as the run would hold it.
func TestNothingToRunHoldsATicketWhoseBlockersBranchGitCantLookUp(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	b := newFakeBeads()
	b.add("A", "closed by hand", 1)
	b.set("A", StatusClosed)
	b.add("B", "blocked by A", 2)
	b.link("B", "A", "blocks")
	g := &gitFails{fakeGit: newFakeGit(c.Base), fail: "HasBranch", failing: true}
	n, err := CheckNothingToRun(context.Background(), c, b, g, g)
	want := &NothingToRun{HeldBlocked: 1, Done: "READY_EMPTY after 0 tickets: nothing ready"}
	if err != nil || !reflect.DeepEqual(n, want) {
		t.Errorf("got  %+v, %v\nwant %+v", n, err, want)
	}
	g.stop()
	if n, err := CheckNothingToRun(context.Background(), c, b, g, g); n != nil || err != nil {
		t.Errorf("git answering: got %+v, %v; want B to run", n, err)
	}
}

// A ticket whose branch git can't look up as it starts isn't started on a guess, a new branch cut
// beside an earlier attempt's or an old one taken for none: the run stops with git's error.
func TestStartingATicketGitCantLookUpTheBranchOfStops(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "to start", 1)
		h.worker("A", finishes("a.txt"))
		o := h.loop()
		o.worktrees = &gitFails{fakeGit: h.mem, fail: "HasBranch", failing: true}
		code := o.Run(t.Context())
		if code != ExitTool || !strings.HasPrefix(o.Final(), "WORKTREE_FAILED") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if want := "git can't tell whether wt/A exists: " + errShowRef.Error(); !strings.Contains(o.Final(), want) {
			t.Errorf("final %q, want it to say %q", o.Final(), want)
		}
		if has, _ := h.mem.HasBranch(context.Background(), "", "wt/A"); has {
			t.Error("wt/A was cut, git not having said there was none")
		}
	})
}
