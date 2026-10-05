package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/git"
)

// mergeFixture is a repository on main with a ticket branch in its own worktree, and an Loop set
// up to merge it.
type mergeFixture struct {
	repo string
	git  func(dir string, args ...string) string
	orch *Loop
	sink *recordSink
}

func newMergeFixture(t *testing.T, check string) *mergeFixture {
	t.Helper()
	repo, run := gitRepo(t)
	run(repo, "branch", "-M", "main")
	if err := os.WriteFile(filepath.Join(repo, "shared.txt"), []byte("line 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(repo, "add", ".")
	run(repo, "commit", "-q", "-m", "shared file")
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordSink{}
	o := &Loop{cfg: Config{Repo: repo, Base: "main", Check: check, LogPath: "log"}, log: log, sink: sink, tabs: noTabs{}, notes: newFakeBeads(),
		checkout: git.Git{}, worktrees: git.Git{}, merger: git.Git{}, history: git.Git{}}
	return &mergeFixture{repo: repo, git: run, orch: o, sink: sink}
}

// ticket makes wt/<id> in its own worktree with one commit writing file.
func (f *mergeFixture) ticket(t *testing.T, id, file, content string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), id)
	f.git(f.repo, "worktree", "add", "-q", "-b", "wt/"+id, wt, "main")
	if err := os.WriteFile(filepath.Join(wt, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f.git(wt, "add", ".")
	f.git(wt, "commit", "-q", "-m", id+": change "+file)
	return wt
}

func (f *mergeFixture) onMain(t *testing.T, file, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(f.repo, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f.git(f.repo, "add", ".")
	f.git(f.repo, "commit", "-q", "-m", "main moves on: "+file)
}

func TestMergeFastForwardsWhenMainHasNotMoved(t *testing.T) {
	f := newMergeFixture(t, "exit 1") // must not run: nothing to re-check
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	if s := f.orch.merge(context.Background(), worker{id: "k-1", br: "wt/k-1", wt: wt, tab: "tab"}); s != nil {
		t.Fatal(s)
	}
	if !strings.Contains(f.git(f.repo, "log", "--oneline", "-1"), "k-1: change a.txt") {
		t.Error("the ticket was not merged")
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Error("the worktree should be removed after merging")
	}
	if !strings.Contains(f.sink.text(), "k-1 closed") {
		t.Errorf("events:\n%s", f.sink.text())
	}
}

func TestMergeRebasesAndRechecksWhenMainMoved(t *testing.T) {
	f := newMergeFixture(t, "test -f a.txt && test -f b.txt") // passes only on the rebased tree
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	f.onMain(t, "b.txt", "b\n")
	if s := f.orch.merge(context.Background(), worker{id: "k-1", br: "wt/k-1", wt: wt, tab: "tab"}); s != nil {
		t.Fatal(s)
	}
	log := f.git(f.repo, "log", "--oneline")
	if !strings.Contains(log, "k-1: change a.txt") || !strings.Contains(log, "main moves on: b.txt") {
		t.Errorf("history:\n%s", log)
	}
	ev := f.sink.text()
	if !strings.Contains(ev, "rebased wt/k-1 onto main") || !strings.Contains(ev, "passes on the rebased") {
		t.Errorf("events:\n%s", ev)
	}
}

func TestMergeLeavesAConflictForReview(t *testing.T) {
	f := newMergeFixture(t, "true")
	wt := f.ticket(t, "k-1", "shared.txt", "line 1 from the ticket\n")
	f.onMain(t, "shared.txt", "line 1 from main\n")
	before := f.git(f.repo, "rev-parse", "main")
	if s := f.orch.merge(context.Background(), worker{id: "k-1", br: "wt/k-1", wt: wt, tab: "tab"}); s != nil {
		t.Fatal(s)
	}
	if f.git(f.repo, "rev-parse", "main") != before {
		t.Error("main must not change on a conflict")
	}
	if !strings.Contains(f.sink.text(), "MERGE_CONFLICT: k-1") {
		t.Errorf("events:\n%s", f.sink.text())
	}
	if st := f.git(wt, "status", "--porcelain"); st != "" {
		t.Errorf("the worktree should be left clean, not mid-rebase: %q", st)
	}
	if got := f.orch.setAside(); len(got) != 1 || got[0] != "k-1" {
		t.Errorf("set aside = %v", got)
	}
}

func TestMergeLeavesAFailingRecheckForReview(t *testing.T) {
	f := newMergeFixture(t, "exit 3")
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	f.onMain(t, "b.txt", "b\n")
	before := f.git(f.repo, "rev-parse", "main")
	if s := f.orch.merge(context.Background(), worker{id: "k-1", br: "wt/k-1", wt: wt, tab: "tab"}); s != nil {
		t.Fatal(s)
	}
	if f.git(f.repo, "rev-parse", "main") != before {
		t.Error("main must not change when the checks fail")
	}
	if !strings.Contains(f.sink.text(), "CHECKS_FAILED: k-1") {
		t.Errorf("events:\n%s", f.sink.text())
	}
}

// A hung check is stopped at the check timeout and its ticket set aside, which frees the merge
// queue for the next finished ticket.
func TestMergeStopsACheckPastItsTimeout(t *testing.T) {
	f := newMergeFixture(t, "if test -f a.txt; then sleep 60; fi") // hangs on k-1 only
	f.orch.cfg.CheckTimeout = 300 * time.Millisecond
	hung := f.ticket(t, "k-1", "a.txt", "a\n")
	next := f.ticket(t, "k-2", "c.txt", "c\n")
	f.onMain(t, "b.txt", "b\n")
	start := time.Now()
	if s := f.orch.merge(context.Background(), worker{id: "k-1", br: "wt/k-1", wt: hung, tab: "tab"}); s != nil {
		t.Fatal(s)
	}
	// The check's process group gets its grace period (5s) and WaitDelay (10s) at most.
	if took := time.Since(start); took > f.orch.cfg.CheckTimeout+15*time.Second {
		t.Errorf("the check was stopped after %s", took)
	}
	ev := f.sink.text()
	if !strings.Contains(ev, "CHECKS_FAILED: k-1 closed, but 'if test -f a.txt; then sleep 60; fi' did not finish within 300ms on wt/k-1") {
		t.Errorf("events:\n%s", ev)
	}
	if got := f.orch.setAside(); len(got) != 1 || got[0] != "k-1" {
		t.Errorf("set aside = %v", got)
	}
	if s := f.orch.merge(context.Background(), worker{id: "k-2", br: "wt/k-2", wt: next, tab: "tab"}); s != nil {
		t.Fatal(s)
	}
	if !strings.Contains(f.sink.text(), "k-2 closed") {
		t.Errorf("k-2 did not merge; events:\n%s", f.sink.text())
	}
}

func TestMergeCheckTimeoutDefaultsTo30Minutes(t *testing.T) {
	f := newMergeFixture(t, "true")
	if got := f.orch.checkTimeout(); got != 30*time.Minute {
		t.Errorf("checkTimeout = %s", got)
	}
}

func TestMergeWithoutACheckCommandSaysSo(t *testing.T) {
	f := newMergeFixture(t, "")
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	f.onMain(t, "b.txt", "b\n")
	if s := f.orch.merge(context.Background(), worker{id: "k-1", br: "wt/k-1", wt: wt, tab: "tab"}); s != nil {
		t.Fatal(s)
	}
	if ev := f.sink.text(); !strings.Contains(ev, "without checking the rebased code") || !strings.Contains(ev, "k-1 closed") {
		t.Errorf("events:\n%s", ev)
	}
}

// A fast-forward moves whichever branch the main checkout is on, so switching it off Base while
// a ticket runs must stop the run rather than land the ticket on the other branch.
func TestMergeStopsWhenTheCheckoutLeftBase(t *testing.T) {
	f := newMergeFixture(t, "true")
	f.git(f.repo, "branch", "other")
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	f.git(f.repo, "switch", "-q", "other")
	main, other := f.git(f.repo, "rev-parse", "main"), f.git(f.repo, "rev-parse", "other")
	s := f.orch.merge(context.Background(), worker{id: "k-1", br: "wt/k-1", wt: wt, tab: "tab"})
	if s == nil || s.code != ExitDirty || s.kind != stopDirtyTree {
		t.Fatalf("stop = %+v, want DIRTY_TREE", s)
	}
	if f.git(f.repo, "rev-parse", "main") != main || f.git(f.repo, "rev-parse", "other") != other {
		t.Error("neither branch may move")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Error("the worktree must be kept for review")
	}
	f.git(f.repo, "rev-parse", "--verify", "-q", "wt/k-1") // fails the test if the branch is gone
	if strings.Contains(f.sink.text(), "k-1 closed") {
		t.Errorf("events:\n%s", f.sink.text())
	}
}

func TestMergeStopsOnUncommittedChangesInTheCheckout(t *testing.T) {
	f := newMergeFixture(t, "true")
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	if err := os.WriteFile(filepath.Join(f.repo, "shared.txt"), []byte("edited by hand\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := f.git(f.repo, "rev-parse", "main")
	s := f.orch.merge(context.Background(), worker{id: "k-1", br: "wt/k-1", wt: wt, tab: "tab"})
	if s == nil || s.code != ExitDirty || s.kind != stopDirtyTree || !strings.Contains(s.Error(), "uncommitted changes") {
		t.Fatalf("stop = %+v, want DIRTY_TREE", s)
	}
	if why := f.orch.unmerged["k-1"]; why != "DIRTY_TREE" {
		t.Errorf("k-1 left unmerged for %q, want DIRTY_TREE", why)
	}
	if f.git(f.repo, "rev-parse", "main") != before {
		t.Error("main must not move")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Error("the worktree must be kept for review")
	}
}

func TestWorkersMergingAtTheSameTimeBothLand(t *testing.T) {
	f := newMergeFixture(t, "true")
	const n = 4
	wts := make([]string, n)
	for i := range wts {
		wts[i] = f.ticket(t, fmt.Sprintf("k-%d", i), fmt.Sprintf("f%d.txt", i), "x\n")
	}
	var wg sync.WaitGroup
	for i := range wts {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if s := f.orch.merge(context.Background(),
				worker{id: fmt.Sprintf("k-%d", i), br: fmt.Sprintf("wt/k-%d", i), wt: wts[i], tab: "tab"}); s != nil {
				t.Errorf("k-%d: %s", i, s)
			}
		}(i)
	}
	wg.Wait()
	log := f.git(f.repo, "log", "--oneline")
	for i := range n {
		if !strings.Contains(log, fmt.Sprintf("k-%d: change f%d.txt", i, i)) {
			t.Errorf("k-%d missing from main:\n%s", i, log)
		}
	}
	if st := f.git(f.repo, "status", "--porcelain"); st != "" {
		t.Errorf("main checkout left dirty: %q", st)
	}
}

// closesWithoutCommit claims the ticket and closes it, committing nothing.
func closesWithoutCommit(w *fakeWorker) AgentState {
	w.claim()
	w.close()
	return "idle"
}

// leavesUncommitted commits file, changes the tracked file edited without committing it, and
// closes the ticket.
func leavesUncommitted(file, edited string) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		w.commit(file)
		if err := os.WriteFile(filepath.Join(w.wt, edited), []byte("changed\n"), 0o644); err != nil {
			w.t.Error(err)
		}
		w.close()
		return "idle"
	}
}

func TestClosedTicketWithUncommittedClaudeChangeIsNotMerged(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	if err := os.MkdirAll(filepath.Join(h.repo, ".claude", "commands"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.repo, ".claude", "commands", "ship.md"), []byte("ship\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.git(h.repo, "add", ".claude")
	h.git(h.repo, "commit", "-q", "-m", "add a project command")
	h.beads.add("A", "first", 1)
	h.worker("A", leavesUncommitted("a.txt", ".claude/commands/ship.md"))
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if ev := h.sink.text(); !strings.Contains(ev, "CLOSED_WITHOUT_COMMIT: A closed") || !strings.Contains(ev, "has uncommitted changes") {
		t.Errorf("events:\n%s", ev)
	}
	if log := h.mainLog(); strings.Contains(log, "A: add a.txt") {
		t.Errorf("A should not be merged:\n%s", log)
	}
	if !exists(h.worktree("A")) {
		t.Error("A's worktree should be left for review")
	}
	if a, _ := h.beads.Show(context.Background(), "A"); !HasLabel(a, UnmergedLabel) {
		t.Errorf("A should be labelled %q: %v", UnmergedLabel, a.Labels)
	}
}
