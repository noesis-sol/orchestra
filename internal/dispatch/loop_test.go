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

func TestPrepareWorktreeReplacesADeletedFolder(t *testing.T) {
	f := newMergeFixture(t, "true")
	old := f.ticket(t, "k-1", "a.txt", "a\n")
	if err := os.RemoveAll(old); err != nil {
		t.Fatal(err)
	}
	f.orch.cfg.WTRoot = t.TempDir()
	wt, s := f.orch.prepareWorktree("k-1", "wt/k-1")
	if s != nil {
		t.Fatal(s.text)
	}
	if want := filepath.Join(f.orch.cfg.WTRoot, "k-1"); wt != want {
		t.Errorf("worktree = %q, want a fresh one at %q", wt, want)
	}
	if got := read(t, filepath.Join(wt, "a.txt")); got != "a\n" {
		t.Errorf("the fresh worktree should be on the existing branch, a.txt = %q", got)
	}
	if br := strings.TrimSpace(f.git(wt, "branch", "--show-current")); br != "wt/k-1" {
		t.Errorf("branch = %q", br)
	}
}

// Fakes for a run whose workers stop before their agents start.

type readyTickets []Ticket

func (r readyTickets) Ready() ([]Ticket, error) { return r, nil }
func (readyTickets) Show(id string) Ticket      { return Ticket{ID: id, Status: "open"} }
func (readyTickets) Status(id string) string    { return "open" }
func (readyTickets) Describe(id string) string  { return id }

type cleanCheckout struct{}

func (cleanCheckout) DirtyTree(dir string) string      { return "" }
func (cleanCheckout) CurrentBranch(repo string) string { return "main" }
func (cleanCheckout) Head(repo, rev string) string     { return "abc" }

// newWorktrees creates every worktree.
type newWorktrees struct{}

func (newWorktrees) WorktreeOf(repo, branch string) string { return "" }
func (newWorktrees) HasBranch(repo, branch string) bool    { return false }
func (newWorktrees) Prune(repo string)                     {}
func (newWorktrees) AddWorktree(repo, path, branch string) (string, error) {
	return "", nil
}
func (newWorktrees) NewWorktree(repo, path, branch, base string) (string, error) {
	return "", nil
}
func (newWorktrees) RemoveWorktree(repo, path string) (string, error) { return "", nil }
func (newWorktrees) DeleteBranch(repo, branch string) (string, error) { return "", nil }

type upToDate struct{}

func (upToDate) IsAncestor(repo, ancestor, rev string) bool            { return true }
func (upToDate) CommitNaming(repo, base, branch, ticket string) string { return "" }
func (upToDate) Rebase(worktree, onto string) (string, error)          { return "", nil }
func (upToDate) AbortRebase(worktree string)                           {}
func (upToDate) FastForward(repo, branch string) (string, error)       { return "", nil }

type noAgents struct{}

func (noAgents) Status(name string) string                             { return "gone" }
func (noAgents) Screen(name string) string                             { return "" }
func (noAgents) Prompt(ctx context.Context, name, prompt string) error { return nil }
func (noAgents) SendKeys(name string, keys ...string) error            { return nil }
func (noAgents) WaitStarted(ctx context.Context, name string) bool     { return true }

// gatedTabs fails to open a tab; for the ticket gated it waits until release is closed first.
type gatedTabs struct {
	gated   string
	release chan struct{}
}

func (g gatedTabs) CreateTab(workspace, cwd, label string) (string, string, error) {
	if label == g.gated {
		<-g.release
	}
	return "", "", fmt.Errorf("no such workspace")
}
func (gatedTabs) CloseTab(tab string) {}

// holdSink records events and closes release on the first HOLD.
type holdSink struct {
	recordSink
	release chan struct{}
	once    sync.Once
}

func (h *holdSink) Event(ev Event) {
	h.recordSink.Event(ev)
	if ev.Kind == EvHold {
		h.once.Do(func() { close(h.release) })
	}
}

func TestEveryWorkersStopReasonIsReported(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	log, err := OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	sink := &holdSink{release: release}
	// Both workers stop for want of a tab: A first, while B runs; B only once A's HOLD is out.
	o := New(Config{Repo: "repo", Base: "main", Workspace: "ws", Limit: 10, Concurrency: 2, WTRoot: "wts", LogPath: "log"},
		log, "", Deps{Tickets: readyTickets{{ID: "A"}, {ID: "B"}}, Tabs: gatedTabs{gated: "B", release: release},
			Agents: noAgents{}, Checkout: cleanCheckout{}, Worktrees: newWorktrees{}, Merger: upToDate{}})
	o.SetSink(sink)
	code := o.Run(context.Background())
	if code != ExitTool {
		t.Errorf("exit code %d, want %d", code, ExitTool)
	}

	var holds []string
	for _, ev := range sink.events {
		if ev.Kind == EvHold {
			holds = append(holds, ev.Ticket+" "+ev.Text)
		}
	}
	wantHolds := []string{
		"A HOLD: TAB_FAILED for A (is 'ws' a valid workspace?); no new tickets while the 1 running finish",
		"B HOLD: TAB_FAILED for B (is 'ws' a valid workspace?)",
	}
	if strings.Join(holds, "\n") != strings.Join(wantHolds, "\n") {
		t.Errorf("HOLD events:\n%s\nwant:\n%s", strings.Join(holds, "\n"), strings.Join(wantHolds, "\n"))
	}
	final := "TAB_FAILED for A (is 'ws' a valid workspace?); also TAB_FAILED for B (is 'ws' a valid workspace?)"
	if o.Final() != final {
		t.Errorf("final line %q, want %q", o.Final(), final)
	}
	logged := read(t, logPath)
	for _, want := range []string{wantHolds[0][2:], wantHolds[1][2:], final} {
		if !strings.Contains(logged, want) {
			t.Errorf("log lacks %q:\n%s", want, logged)
		}
	}
}
