package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
)

// A worker that leaves its ticket's branch, against its rules, has its worktree on another branch or
// a detached HEAD as it closes the ticket. A rebase there would rebase what the worktree has checked
// out, never its branch, which then never comes to be on top of Base: each attempt rebased and
// checked the other branch again, and the merge ended in MERGE_CONFLICT, "main kept changing". The
// ticket is set aside instead, naming what the worktree has checked out, with no check run.

// leavesItsBranch claims the ticket, commits file on its branch, has main move on, then has its
// worktree switch to branch ("" detaches its HEAD), commits there unless detached, and closes it.
func leavesItsBranch(file, branch string) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		w.commit(file)
		w.git.commit("main", "landed on main: "+file+".landed", file+".landed")
		w.git.switchTo(w.wt, branch)
		if branch != "" {
			w.commitAs(file+".more", w.id+": more on "+branch)
		}
		w.close()
		return "idle"
	}
}

func TestATicketWhoseWorktreeLeftItsBranchIsSetAside(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, branch, want string }{
		{"another branch", "fix", "has fix checked out, not wt/A"},
		{"detached", "", "has a detached HEAD, not wt/A"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				dir := t.TempDir()
				h.cfg.Check = countedCheck(dir, "")
				h.beads.add("A", "first", 1)
				h.worker("A", leavesItsBranch("a.txt", tc.branch))
				o, _ := h.run()
				ev := h.sink.text()
				if want := "OFF_BRANCH: A closed, but its worktree " + h.worktree("A") + " " + tc.want +
					"; worktree and tab "; !strings.Contains(ev, want) {
					t.Errorf("events lack %q:\n%s", want, ev)
				}
				if strings.Contains(ev, "MERGE_CONFLICT") {
					t.Errorf("events say MERGE_CONFLICT:\n%s", ev)
				}
				if n := checkRuns(t, dir, "A"); n != 0 {
					t.Errorf("A's check ran %d times, want none", n)
				}
				if got := o.asideList(); !equal(got, []string{"A"}) {
					t.Errorf("set aside %v, want A", got)
				}
				if !unmergedLabel(t, h, "A") {
					t.Error("A should be labelled unmerged")
				}
				if log := h.mem.log("main"); strings.Contains(log, "A: ") {
					t.Errorf("A should not be merged:\n%s", log)
				}
			})
		})
	}
}

// The merge looks again before it rebases, as the worker may have switched since: here after the
// ticket was set aside for its check, before it is checked once more.
func TestMergeOfAWorktreeOffItsBranchRebasesNothing(t *testing.T) {
	t.Parallel()
	h := newTimedHarness(t)
	dir := t.TempDir()
	h.cfg.Check = countedCheck(dir, "")
	h.beads.add("A", "first", 1)
	h.beads.set("A", "closed")
	ctx := context.Background()
	wt := h.worktree("A")
	if _, err := h.mem.NewWorktree(ctx, h.repo, wt, "wt/A", "main"); err != nil {
		t.Fatal(err)
	}
	if err := h.mem.commitIn(wt, "A: add a.txt", "a.txt"); err != nil {
		t.Fatal(err)
	}
	h.mem.commit("main", "landed on main: b.txt", "b.txt")
	h.mem.switchTo(wt, "fix")
	fix, before := h.mem.log("fix"), h.mem.log("main")
	o := h.loop()
	if s := o.merge(ctx, worker{id: "A", br: "wt/A", wt: wt, tab: "tab"}); s != nil {
		t.Fatal(s)
	}
	ev := h.sink.text()
	if want := "OFF_BRANCH: A closed, but its worktree " + wt + " has fix checked out, not wt/A"; !strings.Contains(ev, want) {
		t.Errorf("events lack %q:\n%s", want, ev)
	}
	if n := checkRuns(t, dir, "A"); n != 0 {
		t.Errorf("A's check ran %d times, want none", n)
	}
	if h.mem.log("fix") != fix || h.mem.log("main") != before {
		t.Errorf("nothing should be rebased or merged; fix:\n%s\nmain:\n%s", h.mem.log("fix"), h.mem.log("main"))
	}
	if got := o.asideList(); !equal(got, []string{"A"}) {
		t.Errorf("set aside %v, want A", got)
	}
}
