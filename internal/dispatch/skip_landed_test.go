package dispatch

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// A worker resolving a stopped rebase skips the commits whose change Base has already: its branch
// merges with the commits left, or, with none left, closes with no change.

// The hand-back asks for the check once the rebase has finished, since commits that apply cleanly
// after a resolved one may break it, and says to skip a commit whose change Base has already.
func TestTheResolvePromptChecksAfterTheRebaseAndSkipsWhatLanded(t *testing.T) {
	t.Parallel()
	o := &Loop{cfg: Config{Base: "main", Check: "make check"}}
	p := o.resolvePrompt(rebaseStop{worker: worker{id: "A", br: "wt/A"}, files: []string{"a.go", "b.go"}})
	for _, want := range []string{
		"rebasing wt/A onto it stopped on conflicts in a.go, b.go",
		"If main has a commit's change already, wholly or as the part that conflicts, run git rebase --skip",
		"Once the rebase has finished, run 'make check' in the foreground until it passes",
		"git commit --amend --no-edit",
		"Say DONE when the rebase is complete and the check passes.",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("no %q in the prompt:\n%s", want, p)
		}
	}
	if i, j := strings.Index(p, "rebase --continue"), strings.Index(p, "'make check'"); i < 0 || j < i {
		t.Errorf("the check should come after the rebase continues:\n%s", p)
	}
}

// skipsRebase resolves the stopped rebase by skipping the commit it stopped on, whose change main
// has already, and lets the rebase go on with the rest.
func skipsRebase(w *fakeWorker) AgentState {
	w.gitIn(w.wt, "rebase", "--skip")
	return "idle"
}

// A worker that skips the conflicting commit leaves the branch with fewer commits than the ticket
// had: those left are checked and merge.
func TestASkippedCommitMergesTheRest(t *testing.T) {
	t.Parallel()
	h := conflictHarness(t)
	h.beads.add("A", "first", 1)
	h.worker("A", func(w *fakeWorker) AgentState {
		conflicting(h.repo)(w)
		w.commit("a.txt")
		return "idle"
	}, skipsRebase)
	o, code := h.run()
	ev := h.sink.text()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), ev)
	}
	for _, want := range []string{"RESOLVING: wt/A conflicts with main in shared.txt", "A closed"} {
		if !strings.Contains(ev, want) {
			t.Errorf("no %q in events:\n%s", want, ev)
		}
	}
	if strings.Contains(ev, "MERGE_CONFLICT") || strings.Contains(ev, "no change") {
		t.Errorf("A should merge its other commit:\n%s", ev)
	}
	if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || strings.Contains(log, "A: add shared.txt") {
		t.Errorf("only A's commit main hasn't should reach main:\n%s", log)
	}
	if got := read(t, filepath.Join(h.repo, "shared.txt")); got != "main\n" {
		t.Errorf("main's shared.txt should be left as it is: %q", got)
	}
	if a, _ := h.beads.Show(context.Background(), "A"); HasLabel(a, UnmergedLabel) {
		t.Errorf("A should not be labelled %q", UnmergedLabel)
	}
}

// A worker that skips every commit leaves nothing to merge: the ticket closes with no change, without
// running the check on code it doesn't change, and the ticket it blocks goes on.
func TestEveryCommitSkippedClosesUnchanged(t *testing.T) {
	t.Parallel()
	h := conflictHarness(t)
	h.cfg.Check = "exit 1" // any check run on A's code would set it aside
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.beads.link("B", "A", "blocks")
	h.worker("A", conflicting(h.repo), skipsRebase)
	h.worker("B", finishes("b.txt"))
	o, code := h.run()
	ev := h.sink.text()
	if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), ev)
	}
	if !strings.Contains(ev, "A closed with no change: its worker found the changes of wt/A on main already "+
		"and skipped them in the rebase, so there is nothing to merge; worktree, branch and tab removed") {
		t.Errorf("want A closed with no change:\n%s", ev)
	}
	for _, not := range []string{"MERGE_CONFLICT", "CHECKS_FAILED", "checking it with"} {
		if strings.Contains(ev, not) {
			t.Errorf("A should be neither checked nor set aside (%q):\n%s", not, ev)
		}
	}
	if exists(h.worktree("A")) || h.git(h.repo, "branch", "--list", "wt/A") != "" {
		t.Error("A's worktree and branch should be removed")
	}
	if a, _ := h.beads.Show(context.Background(), "A"); HasLabel(a, UnmergedLabel) {
		t.Errorf("A should not be labelled %q: %v", UnmergedLabel, a.Labels)
	}
	if log := h.mainLog(); strings.Contains(log, "A: add") || !strings.Contains(log, "B: add b.txt") {
		t.Errorf("none of A's commits should reach main, and B should merge:\n%s", log)
	}
}
