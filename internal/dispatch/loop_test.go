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

// recordSink keeps events for assertions.
type recordSink struct {
	mu     sync.Mutex
	events []Event
}

func (r *recordSink) Event(ev Event) { r.mu.Lock(); r.events = append(r.events, ev); r.mu.Unlock() }
func (r *recordSink) Status(Status)  {}
func (r *recordSink) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, ev := range r.events {
		b.WriteString(ev.Text + "\n")
	}
	return b.String()
}

// noTabs stands in for Herdr's tabs: merging closes the worker's tab.
type noTabs struct{}

func (noTabs) CreateTab(workspace, cwd, label string) (string, string, error) {
	return "tab", "pane", nil
}
func (noTabs) CloseTab(tab string) {}

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
	os.WriteFile(filepath.Join(repo, "shared.txt"), []byte("line 1\n"), 0o644)
	run(repo, "add", ".")
	run(repo, "commit", "-q", "-m", "shared file")
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	sink := &recordSink{}
	o := &Loop{cfg: Config{Repo: repo, Base: "main", Check: check, LogPath: "log"}, log: log, sink: sink, tabs: noTabs{},
		checkout: git.Git{}, worktrees: git.Git{}, merger: git.Git{}, history: git.Git{}}
	return &mergeFixture{repo: repo, git: run, orch: o, sink: sink}
}

// ticket makes wt/<id> in its own worktree with one commit writing file.
func (f *mergeFixture) ticket(t *testing.T, id, file, content string) string {
	t.Helper()
	wt := filepath.Join(t.TempDir(), id)
	f.git(f.repo, "worktree", "add", "-q", "-b", "wt/"+id, wt, "main")
	os.WriteFile(filepath.Join(wt, file), []byte(content), 0o644)
	f.git(wt, "add", ".")
	f.git(wt, "commit", "-q", "-m", id+": change "+file)
	return wt
}

func (f *mergeFixture) onMain(t *testing.T, file, content string) {
	t.Helper()
	os.WriteFile(filepath.Join(f.repo, file), []byte(content), 0o644)
	f.git(f.repo, "add", ".")
	f.git(f.repo, "commit", "-q", "-m", "main moves on: "+file)
}

func TestMergeFastForwardsWhenMainHasNotMoved(t *testing.T) {
	f := newMergeFixture(t, "exit 1") // must not run: nothing to re-check
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	if s := f.orch.merge(context.Background(), "k-1", "wt/k-1", wt, "tab"); s != nil {
		t.Fatal(s.text)
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
	if s := f.orch.merge(context.Background(), "k-1", "wt/k-1", wt, "tab"); s != nil {
		t.Fatal(s.text)
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
	if s := f.orch.merge(context.Background(), "k-1", "wt/k-1", wt, "tab"); s != nil {
		t.Fatal(s.text)
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
	f.orch.merge(context.Background(), "k-1", "wt/k-1", wt, "tab")
	if f.git(f.repo, "rev-parse", "main") != before {
		t.Error("main must not change when the checks fail")
	}
	if !strings.Contains(f.sink.text(), "CHECKS_FAILED: k-1") {
		t.Errorf("events:\n%s", f.sink.text())
	}
}

func TestMergeWithoutACheckCommandSaysSo(t *testing.T) {
	f := newMergeFixture(t, "")
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	f.onMain(t, "b.txt", "b\n")
	f.orch.merge(context.Background(), "k-1", "wt/k-1", wt, "tab")
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
	s := f.orch.merge(context.Background(), "k-1", "wt/k-1", wt, "tab")
	if s == nil || s.code != ExitDirty || !strings.HasPrefix(s.text, "DIRTY_TREE:") {
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
	os.WriteFile(filepath.Join(f.repo, "shared.txt"), []byte("edited by hand\n"), 0o644)
	before := f.git(f.repo, "rev-parse", "main")
	s := f.orch.merge(context.Background(), "k-1", "wt/k-1", wt, "tab")
	if s == nil || s.code != ExitDirty || !strings.Contains(s.text, "uncommitted changes") {
		t.Fatalf("stop = %+v, want DIRTY_TREE", s)
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
			if s := f.orch.merge(context.Background(), fmt.Sprintf("k-%d", i), fmt.Sprintf("wt/k-%d", i), wts[i], "tab"); s != nil {
				t.Errorf("k-%d: %s", i, s.text)
			}
		}(i)
	}
	wg.Wait()
	log := f.git(f.repo, "log", "--oneline")
	for i := 0; i < n; i++ {
		if !strings.Contains(log, fmt.Sprintf("k-%d: change f%d.txt", i, i)) {
			t.Errorf("k-%d missing from main:\n%s", i, log)
		}
	}
	if st := f.git(f.repo, "status", "--porcelain"); st != "" {
		t.Errorf("main checkout left dirty: %q", st)
	}
}

func TestPickNextSkipsRunningTickets(t *testing.T) {
	ready := []Ticket{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	if tk, q := pickNext(ready, map[string]bool{"a": true}); tk == nil || tk.ID != "b" || q != 1 {
		t.Errorf("got %v, %d", tk, q)
	}
	if tk, _ := pickNext(ready, map[string]bool{"a": true, "b": true, "c": true}); tk != nil {
		t.Errorf("everything is running, got %v", tk)
	}
}

func TestOutcomes(t *testing.T) {
	for status, want := range map[string]outcome{
		"closed": outcomeClosed, "deferred": outcomeDeferred, "in_progress": outcomePaused,
		"unknown": outcomeUnreadable, "open": outcomeUnfinished, "blocked": outcomeUnfinished,
	} {
		if got := outcomeOf(status); got != want {
			t.Errorf("outcomeOf(%q) = %v, want %v", status, got, want)
		}
	}
	if closedOutcomeOf("", false) != closedNoCommit || closedOutcomeOf("", true) != closedNoCommit {
		t.Error("a closed ticket without a commit must not merge")
	}
	if closedOutcomeOf("abc123 fix", true) != closedDirty {
		t.Error("a dirty worktree must not merge")
	}
	if closedOutcomeOf("abc123 fix", false) != closedMerge {
		t.Error("a commit and a clean worktree should merge")
	}
}

func TestNotifiable(t *testing.T) {
	if !notifiable("  kinieta-x closed (abc); merged") || !notifiable("PAUSED: x") {
		t.Error("closed and PAUSED should notify")
	}
	if notifiable("[1/40] kinieta-x dispatching: Title") || notifiable("  worktree /a on wt/x") {
		t.Error("dispatch and worktree lines should not notify")
	}
}

func TestIdleWorkerWithTicketInProgressGetsGrace(t *testing.T) {
	cases := []struct {
		status string
		idle   time.Duration
		wait   bool
	}{
		{"in_progress", time.Minute, true},              // probably waiting on its own background command
		{"in_progress", idleGrace + time.Second, false}, // long enough: it needs someone
		{"closed", 0, false}, {"deferred", 0, false}, {"open", 0, false},
	}
	for _, c := range cases {
		if got := keepWaiting(c.status, c.idle); got != c.wait {
			t.Errorf("keepWaiting(%s, %s) = %v, want %v", c.status, c.idle, got, c.wait)
		}
	}
}
