package dispatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/git"
	"github.com/noesis-sol/orchestra/internal/organ"
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
	o := &Loop{cfg: Config{Repo: repo, Base: "main", Check: check, LogPath: "log"}, log: log, sink: sink, tabs: noTabs{}, notes: newFakeBeads(),
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

func TestPickNextSkipsRunningAndHeldTickets(t *testing.T) {
	ready := []Ticket{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	none := func(Ticket) bool { return false }
	if tk, q := pickNext(ready, map[string]bool{"a": true}, none); tk == nil || tk.ID != "b" || q != 2 {
		t.Errorf("got %v, %d", tk, q)
	}
	if tk, _ := pickNext(ready, map[string]bool{"a": true, "b": true, "c": true, "d": true}, none); tk != nil {
		t.Errorf("everything is running, got %v", tk)
	}
	heldB := func(t Ticket) bool { return t.ID == "b" }
	if tk, q := pickNext(ready, map[string]bool{"a": true}, heldB); tk == nil || tk.ID != "c" || q != 1 {
		t.Errorf("b is held, got %v, %d", tk, q)
	}
}

// fakeTickets stands in for Beads: a ready queue, and each ticket's dependencies for Show.
type fakeTickets struct {
	ready []Ticket
	shown map[string]Ticket
}

func (f fakeTickets) Ready() ([]Ticket, error) { return f.ready, nil }
func (f fakeTickets) Show(id string) (Ticket, error) {
	if t, ok := f.shown[id]; ok {
		return t, nil
	}
	return Ticket{ID: id, Status: "unknown"}, fmt.Errorf("bd show %s: not found", id)
}
func (f fakeTickets) Status(id string) (string, error) {
	t, err := f.Show(id)
	return t.Status, err
}
func (f fakeTickets) Describe(id string) string { return id }
func (f fakeTickets) Closed(label string) ([]Ticket, error) {
	var closed []Ticket
	for _, t := range f.shown {
		if t.Status == "closed" && HasLabel(t, label) {
			closed = append(closed, t)
		}
	}
	return closed, nil
}

// aBlocksB is Beads with A closed (so bd ready lists B, which A blocks) and C ready on its own.
func aBlocksB() fakeTickets {
	b := Ticket{ID: "k-b", Status: "open", Dependencies: []Ticket{
		{ID: "k-x", Status: "open", DependencyType: "related"},
		{ID: "k-a", Status: "closed", DependencyType: "blocks"},
	}}
	return fakeTickets{ready: []Ticket{{ID: "k-b", Status: "open"}},
		shown: map[string]Ticket{"k-b": b, "k-c": {ID: "k-c", Status: "open"}}}
}

func (f *mergeFixture) next(t *testing.T, running map[string]bool) string {
	t.Helper()
	tk, _, s := f.orch.next(running)
	if s != nil {
		t.Fatal(s.text)
	}
	if tk == nil {
		return ""
	}
	return tk.ID
}

func TestDependentWaitsWhileItsBlockerIsInFlight(t *testing.T) {
	f := newMergeFixture(t, "true")
	tk := aBlocksB()
	tk.ready = append(tk.ready, Ticket{ID: "k-c", Status: "open"})
	f.orch.tickets = tk
	running := map[string]bool{"k-a": true} // closed, waiting in the merge queue
	if got := f.next(t, running); got != "k-c" {
		t.Errorf("next = %q, want k-c while k-a has not merged", got)
	}
	running["k-c"] = true
	if got := f.next(t, running); got != "" {
		t.Errorf("next = %q, want nothing", got)
	}
	if ev := f.sink.text(); strings.Count(ev, "k-b waits: waiting for k-a to merge") != 1 {
		t.Errorf("the wait should be said once; events:\n%s", ev)
	}
	delete(running, "k-a") // merged
	if got := f.next(t, running); got != "k-b" {
		t.Errorf("next = %q, want k-b once k-a merged", got)
	}
}

func TestDependentWaitsWhileItsBlockerIsUnmerged(t *testing.T) {
	cases := []struct {
		name  string
		check string
		setup func(f *mergeFixture) string // makes k-a's branch, returns its worktree
		why   string
	}{
		{"conflict", "true", func(f *mergeFixture) string {
			wt := f.ticket(t, "k-a", "shared.txt", "line 1 from the ticket\n")
			f.onMain(t, "shared.txt", "line 1 from main\n")
			return wt
		}, "MERGE_CONFLICT"},
		{"checks fail", "exit 3", func(f *mergeFixture) string {
			wt := f.ticket(t, "k-a", "a.txt", "a\n")
			f.onMain(t, "b.txt", "b\n")
			return wt
		}, "CHECKS_FAILED"},
		{"no commit", "true", func(f *mergeFixture) string {
			wt := filepath.Join(t.TempDir(), "k-a")
			f.git(f.repo, "worktree", "add", "-q", "-b", "wt/k-a", wt, "main")
			return wt
		}, "CLOSED_WITHOUT_COMMIT"},
		{"dirty", "true", func(f *mergeFixture) string {
			wt := f.ticket(t, "k-a", "a.txt", "a\n")
			os.WriteFile(filepath.Join(wt, "a.txt"), []byte("unfinished\n"), 0o644)
			return wt
		}, "CLOSED_WITHOUT_COMMIT"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newMergeFixture(t, c.check)
			f.orch.tickets = aBlocksB()
			wt := c.setup(f)
			if s := f.orch.finish(context.Background(), "k-a", "wt/k-a", wt, "tab"); s != nil {
				t.Fatal(s.text)
			}
			if got := f.next(t, nil); got != "" {
				t.Errorf("next = %q, want k-b held while k-a is unmerged", got)
			}
			if ev := f.sink.text(); !strings.Contains(ev, "k-b waits: k-a closed but not merged ("+c.why+")") {
				t.Errorf("events:\n%s", ev)
			}
		})
	}
}

func TestDependentStartsOnceItsBlockerMerges(t *testing.T) {
	f := newMergeFixture(t, "exit 3")
	f.orch.tickets = aBlocksB()
	wt := f.ticket(t, "k-a", "a.txt", "a\n")
	f.onMain(t, "b.txt", "b\n")
	f.orch.finish(context.Background(), "k-a", "wt/k-a", wt, "tab") // CHECKS_FAILED
	if got := f.next(t, nil); got != "" {
		t.Fatalf("next = %q, want k-b held", got)
	}
	f.orch.cfg.Check = "true" // fixed by hand, and merged
	if s := f.orch.finish(context.Background(), "k-a", "wt/k-a", wt, "tab"); s != nil {
		t.Fatal(s.text)
	}
	if !strings.Contains(f.sink.text(), "k-a closed") {
		t.Fatalf("k-a did not merge; events:\n%s", f.sink.text())
	}
	if got := f.next(t, nil); got != "k-b" {
		t.Errorf("next = %q, want k-b once k-a merged", got)
	}
}

func TestDependentWaitsWhenItsDependenciesCannotBeRead(t *testing.T) {
	f := newMergeFixture(t, "true")
	f.orch.tickets = fakeTickets{ready: []Ticket{{ID: "k-b", Status: "open"}}}
	if got := f.next(t, map[string]bool{"k-a": true}); got != "" {
		t.Errorf("next = %q, want k-b held", got)
	}
	if got := f.next(t, nil); got != "k-b" {
		t.Errorf("next = %q, want k-b: with nothing running or unmerged there is nothing to wait for", got)
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
		if got := keepWaiting(c.status, c.idle, idleGrace); got != c.wait {
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

func (r readyTickets) Ready() ([]Ticket, error)            { return r, nil }
func (readyTickets) Show(id string) (Ticket, error)        { return Ticket{ID: id, Status: "open"}, nil }
func (readyTickets) Status(id string) (string, error)      { return "open", nil }
func (readyTickets) Describe(id string) string             { return id }
func (readyTickets) Closed(label string) ([]Ticket, error) { return nil, nil }

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
func (upToDate) CommitNamingOn(repo, rev, ticket string) string        { return "" }
func (upToDate) Rebase(worktree, onto string) (string, error)          { return "", nil }
func (upToDate) AbortRebase(worktree string)                           {}
func (upToDate) FastForward(repo, branch string) (string, error)       { return "", nil }

type noAgents struct{}

func (noAgents) Status(name string) (string, error)                    { return "gone", nil }
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

// Fakes for a run whose workers start, against a bd that fails.

// errBd is what the fakes' bd says when it fails.
var errBd = fmt.Errorf("bd defer A: exit status 1: Error: database is locked\n  (another bd holds it)")

// brokenBd lists its tickets as ready but can't show their status, defer, note or reopen them.
type brokenBd []Ticket

func (b brokenBd) Ready() ([]Ticket, error)            { return b, nil }
func (brokenBd) Show(id string) (Ticket, error)        { return Ticket{ID: id, Status: "unknown"}, errBd }
func (brokenBd) Status(id string) (string, error)      { return "unknown", errBd }
func (brokenBd) Describe(id string) string             { return id }
func (brokenBd) AppendNotes(id, note string) error     { return errBd }
func (brokenBd) Defer(id, reason string) error         { return errBd }
func (brokenBd) Reopen(id string) error                { return errBd }
func (brokenBd) AddLabel(id, label string) error       { return errBd }
func (brokenBd) RemoveLabel(id, label string) error    { return errBd }
func (brokenBd) Closed(label string) ([]Ticket, error) { return nil, nil } // so the run gets as far as the workers

type okTabs struct{}

func (okTabs) CreateTab(workspace, cwd, label string) (string, string, error) {
	return "tab-" + label, "pane-" + label, nil
}
func (okTabs) CloseTab(tab string) {}

// Fakes for a run whose worker defers its ticket; the loop blocks gathering the evidence for triage
// (Describe) until release is closed.

type okStarter struct{}

func (okStarter) LaunchInPane(pane, kind string, args []string) error { return nil }
func (okStarter) StartAgent(ctx context.Context, name, kind, pane string, args []string) error {
	return nil
}
func (okStarter) IsArgumentRefused(err error) bool                { return false }
func (okStarter) IsNameRefused(err error) bool                    { return false }
func (okStarter) WaitReady(ctx context.Context, name string) bool { return true }

type deferringTickets struct {
	entered, release chan struct{}
	once             *sync.Once
}

func (d deferringTickets) Ready() ([]Ticket, error) { return []Ticket{{ID: "A", Title: "a"}}, nil }
func (deferringTickets) Show(id string) (Ticket, error) {
	return Ticket{ID: id, Status: "deferred"}, nil
}
func (deferringTickets) Status(id string) (string, error)      { return "deferred", nil }
func (deferringTickets) Closed(label string) ([]Ticket, error) { return nil, nil }
func (d deferringTickets) Describe(id string) string {
	d.once.Do(func() { close(d.entered) })
	<-d.release
	return id
}

type quietHistory struct{}

func (quietHistory) ShortStatus(worktree string) string { return "" }
func (quietHistory) OneLineLog(dir, revs string) string { return "" }
func (quietHistory) DiffStat(worktree string) string    { return "" }
func (quietHistory) Subjects(repo, revs string) string  { return "" }

// goneSink records events and closes gone when a worker's status is removed, its last act.
type goneSink struct {
	recordSink
	gone chan struct{}
	once sync.Once
}

func (g *goneSink) Status(st Status) {
	if st.Gone {
		g.once.Do(func() { close(g.gone) })
	}
}

func newDeferringLoop(t *testing.T) (*Loop, deferringTickets, *goneSink) {
	t.Helper()
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	tk := deferringTickets{entered: make(chan struct{}), release: make(chan struct{}), once: &sync.Once{}}
	sink := &goneSink{gone: make(chan struct{})}
	o := New(Config{Repo: "repo", Base: "main", Workspace: "ws", Limit: 1, Concurrency: 1, WTRoot: "wts", LogPath: "log",
		AgentKind: "claude"}, log, "", Deps{Tickets: tk, Tabs: noTabs{}, Starter: okStarter{}, Agents: noAgents{},
		Checkout: cleanCheckout{}, Worktrees: newWorktrees{}, Merger: upToDate{}, History: quietHistory{},
		Advisor: organ.Client{Bin: filepath.Join(t.TempDir(), "no-claude")}, AdviceCtx: context.Background()})
	o.SetSink(sink)
	o.StartTriage()
	return o, tk, sink
}

// Ctrl+C while a worker gathers a deferral for triage: Run waits for the worker before returning,
// so triage closes and the reviewer reads the loop only once it has stopped changing.
func TestInterruptedRunWaitsForItsWorkers(t *testing.T) {
	o, tk, sink := newDeferringLoop(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	codes := make(chan int, 1)
	go func() { codes <- o.Run(ctx) }()
	<-tk.entered
	cancel()
	select {
	case <-codes:
		t.Fatal("Run returned while its worker was still running")
	case <-time.After(100 * time.Millisecond):
	}
	close(tk.release)
	if code := <-codes; code != ExitInterrupted {
		t.Errorf("exit code %d, want %d", code, ExitInterrupted)
	}
	select {
	case <-sink.gone:
	default:
		t.Error("Run returned before its worker did")
	}
	finished := make(chan struct{})
	go func() { o.FinishTriage(context.Background()); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("FinishTriage did not return")
	}
	if strings.Contains(sink.text(), "TRIAGE") {
		t.Errorf("a deferral after Ctrl+C should not be triaged:\n%s", sink.text())
	}
}

// A worker that outlasts the wait finds triage closed when it gets there, and neither panics nor
// blocks.
func TestWorkerOutlastingTheSettleWaitFindsTriageClosed(t *testing.T) {
	o, tk, sink := newDeferringLoop(t)
	o.wait.settle = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	codes := make(chan int, 1)
	go func() { codes <- o.Run(ctx) }()
	<-tk.entered
	cancel()
	select {
	case code := <-codes:
		if code != ExitInterrupted {
			t.Errorf("exit code %d, want %d", code, ExitInterrupted)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its settle wait")
	}
	o.FinishTriage(context.Background())
	close(tk.release)
	select {
	case <-sink.gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker did not return")
	}
}

func TestTriageQueuedAfterFinishIsDropped(t *testing.T) {
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	o := &Loop{log: log, sink: &recordSink{}, organ: organ.Client{Bin: filepath.Join(t.TempDir(), "no-claude")},
		organCtx: context.Background()}
	o.queueTriage(context.Background(), organ.Deferral{ID: "before-start"}) // triage off: nothing happens
	o.StartTriage()
	o.queueTriage(context.Background(), organ.Deferral{ID: "A"})
	o.FinishTriage(context.Background())
	o.queueTriage(context.Background(), organ.Deferral{ID: "B"})
	o.FinishTriage(context.Background()) // a second call returns too
	got := o.sink.(*recordSink).text()
	if !strings.Contains(got, "TRIAGE_FAILED for A") || strings.Contains(got, " B:") || strings.Contains(got, "before-start") {
		t.Errorf("A should be triaged (and fail, without claude), B and before-start dropped:\n%s", got)
	}
	if len(o.triageQ) != 0 {
		t.Errorf("queue = %v", o.triageQ)
	}
}

// scriptedAgents reports the statuses in its script in turn, then gone; "unreadable" fails as a
// Herdr call does.
type scriptedAgents struct {
	noAgents
	script []string
	reads  int
}

func (a *scriptedAgents) Status(name string) (string, error) {
	a.reads++
	if len(a.script) == 0 {
		return "gone", nil
	}
	st := a.script[0]
	a.script = a.script[1:]
	if st == "unreadable" {
		return st, errors.New("herdr agent get: server busy")
	}
	return st, nil
}

func newSettleLoop(t *testing.T, script ...string) (*Loop, *scriptedAgents, string) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	log, err := OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	a := &scriptedAgents{script: script}
	return &Loop{log: log, sink: &recordSink{}, agents: a, tickets: readyTickets{}, wait: timing{poll: time.Millisecond}}, a, logPath
}

// A status Herdr fails to read once says nothing about the worker: the wait goes on.
func TestFailedStatusReadDoesNotEndTheWait(t *testing.T) {
	o, a, logPath := newSettleLoop(t, "working", "unreadable", "working")
	if stop := o.waitSettled(context.Background(), "A", "A", "tab"); stop != nil {
		t.Fatalf("stopped: %s", stop.text)
	}
	if a.reads != 4 {
		t.Errorf("read the status %d times, want 4: the wait should last until the worker is gone", a.reads)
	}
	if logged := read(t, logPath); !strings.Contains(logged, "server busy") {
		t.Errorf("the failed read should be logged:\n%s", logged)
	}
}

func TestStatusUnreadableForLongStopsTheRun(t *testing.T) {
	script := make([]string, maxFailedReads+5)
	for i := range script {
		script[i] = "unreadable"
	}
	o, a, _ := newSettleLoop(t, script...)
	stop := o.waitSettled(context.Background(), "A", "A", "tab")
	if stop == nil || stop.code != ExitTool || !strings.Contains(stop.text, "HERDR_FAILED") {
		t.Fatalf("stop = %+v, want HERDR_FAILED", stop)
	}
	if a.reads != maxFailedReads {
		t.Errorf("read the status %d times, want %d", a.reads, maxFailedReads)
	}
}

// onceReady lists A once; every later 'bd ready' fails.
type onceReady struct {
	readyTickets
	calls *atomic.Int32
}

func (o onceReady) Ready() ([]Ticket, error) {
	if o.calls.Add(1) == 1 {
		return []Ticket{{ID: "A"}}, nil
	}
	return nil, fmt.Errorf("bd ready failed")
}

func TestDispatchTimeStopWithTicketsInFlightHolds(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	log, err := OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	sink := &holdSink{release: release}
	// A is dispatched and runs until the HOLD is out; filling the second slot finds bd ready unreadable.
	o := New(Config{Repo: "repo", Base: "main", Workspace: "ws", Limit: 10, Concurrency: 2, WTRoot: "wts", LogPath: "log"},
		log, "", Deps{Tickets: onceReady{calls: new(atomic.Int32)}, Tabs: gatedTabs{gated: "A", release: release},
			Agents: noAgents{}, Checkout: cleanCheckout{}, Worktrees: newWorktrees{}, Merger: upToDate{}})
	o.SetSink(sink)
	// Without the HOLD, A would wait forever; let it go so the test fails rather than hangs.
	late := time.AfterFunc(5*time.Second, func() { sink.once.Do(func() { close(release) }) })
	defer late.Stop()
	if code := o.Run(context.Background()); code != ExitTool {
		t.Errorf("exit code %d, want %d", code, ExitTool)
	}

	var holds []string
	for _, ev := range sink.events {
		if ev.Kind == EvHold {
			holds = append(holds, ev.Ticket+"|"+ev.Text)
		}
	}
	want := "|HOLD: READY_UNREADABLE: could not read 'bd ready --json': bd ready failed; no new tickets while the 1 running finish"
	if len(holds) == 0 || holds[0] != want {
		t.Errorf("HOLD events:\n%s\nwant first:\n%s", strings.Join(holds, "\n"), want)
	}
	if logged := read(t, logPath); !strings.Contains(logged, want[1:]) {
		t.Errorf("log lacks %q:\n%s", want[1:], logged)
	}
}

// promptAgents take their prompt (or refuse it, with promptErr) and are gone once they have.
type promptAgents struct{ promptErr error }

func (promptAgents) Status(name string) (string, error) { return "gone", nil }
func (promptAgents) Screen(name string) string          { return "" }
func (a promptAgents) Prompt(ctx context.Context, name, prompt string) error {
	return a.promptErr
}
func (promptAgents) SendKeys(name string, keys ...string) error        { return nil }
func (promptAgents) WaitStarted(ctx context.Context, name string) bool { return false }

func brokenBdRun(t *testing.T, agents Agents) (*Loop, *recordSink, string, int) {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	log, err := OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	bd := brokenBd{{ID: "A", Title: "a"}}
	sink := &recordSink{}
	o := New(Config{Repo: "repo", Base: "main", Workspace: "ws", Limit: 5, Concurrency: 1, WTRoot: "wts", LogPath: "log", AgentKind: "claude"},
		log, "", Deps{Tickets: bd, Notes: bd, Tabs: okTabs{}, Starter: okStarter{}, Agents: agents,
			Checkout: cleanCheckout{}, Worktrees: newWorktrees{}, Merger: upToDate{}})
	o.SetSink(sink)
	code := o.Run(context.Background())
	return o, sink, read(t, logPath), code
}

func TestAFailedDeferKeepsTheTicketOutOfTheRun(t *testing.T) {
	// The worker never takes its prompt, and bd can't defer the ticket, so bd still lists it as
	// ready: it must not be dispatched again in this run.
	o, sink, logged, code := brokenBdRun(t, promptAgents{promptErr: fmt.Errorf("paste lost")})
	if code != ExitOK {
		t.Errorf("exit code %d, want %d", code, ExitOK)
	}
	var dispatched, deferred int
	var warned bool
	for _, ev := range sink.events {
		switch ev.Kind {
		case EvDispatch:
			dispatched++
		case EvDeferred:
			deferred++
		case EvWarn:
			warned = ev.Ticket == "A" && strings.Contains(ev.Text, "DEFER_FAILED") &&
				strings.Contains(ev.Text, "database is locked (another bd holds it)")
		}
	}
	if dispatched != 1 {
		t.Errorf("A dispatched %d times, want once:\n%s", dispatched, sink.text())
	}
	if deferred != 0 || !warned {
		t.Errorf("want a DEFER_FAILED warning with bd's error and no 'deferred' line:\n%s", sink.text())
	}
	if !strings.HasPrefix(o.Final(), "READY_EMPTY") {
		t.Errorf("final line %q", o.Final())
	}
	if !strings.Contains(logged, "database is locked") {
		t.Errorf("log lacks bd's error:\n%s", logged)
	}
}

func TestStatusUnreadableSaysWhy(t *testing.T) {
	o, _, logged, code := brokenBdRun(t, promptAgents{})
	if code != ExitTool {
		t.Errorf("exit code %d, want %d", code, ExitTool)
	}
	want := "STATUS_UNREADABLE for A: bd defer A: exit status 1: Error: database is locked (another bd holds it); stopping"
	if !strings.HasPrefix(o.Final(), want) {
		t.Errorf("final line %q, want it to start with %q", o.Final(), want)
	}
	if !strings.Contains(logged, want) {
		t.Errorf("log lacks %q:\n%s", want, logged)
	}
}

// unreadableReady can't list the ready queue.
type unreadableReady struct{ brokenBd }

func (unreadableReady) Ready() ([]Ticket, error) { return nil, errBd }

func TestReadyUnreadableSaysWhy(t *testing.T) {
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	o := New(Config{Repo: "repo", Base: "main"}, log, "", Deps{Tickets: unreadableReady{}, Checkout: cleanCheckout{}})
	_, _, s := o.next(nil)
	want := "READY_UNREADABLE: could not read 'bd ready --json': bd defer A: exit status 1: Error: database is locked (another bd holds it)"
	if s == nil || s.text != want {
		t.Errorf("got %+v, want %q", s, want)
	}
}

// A ticket set aside in this run stays out of it, except one that waited on a question: bd lists
// it as ready again only once the question is answered, and then it comes back. Set aside again
// after that, it stays out.
func TestSetAsideTicketsStayOutUnlessTheirQuestionWasAnswered(t *testing.T) {
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	ready := readyTickets{{ID: "deferred"}, {ID: "answered"}, {ID: "fresh"}}
	o := New(Config{Repo: "repo", Base: "main"}, log, "", Deps{Tickets: ready, Checkout: cleanCheckout{}})
	o.SetSink(&recordSink{})
	o.markAside("deferred")
	o.markAside("answered")
	o.setAsked("answered", true)
	if tk, _, s := o.next(nil); s != nil || tk == nil || tk.ID != "answered" {
		t.Fatalf("got %v (%v), want the ticket whose question was answered", tk, s)
	}
	o.setAsked("answered", false) // dispatched again, then set aside for another reason
	if tk, _, s := o.next(nil); s != nil || tk == nil || tk.ID != "fresh" {
		t.Fatalf("got %v (%v), want the fresh ticket", tk, s)
	}
}

func TestAnUnreadableStatusIsNotAClaim(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "orchestra.log")
	log, err := OpenLog(logPath, false, "t")
	if err != nil {
		t.Fatal(err)
	}
	o := New(Config{}, log, "", Deps{Tickets: brokenBd{}})
	if o.claimed("A") {
		t.Error("an unreadable status was taken for a claim")
	}
	if !strings.Contains(read(t, logPath), "database is locked") {
		t.Error("the cause was not logged")
	}
}

// labelledA is Beads with k-a closed and labelled unmerged by an earlier run, blocking k-b.
func labelledA() fakeTickets {
	tk := aBlocksB()
	tk.shown["k-a"] = Ticket{ID: "k-a", Status: "closed", Labels: []string{UnmergedLabel}}
	return tk
}

func TestUnmergedLabelStaysUntilACommitNamingTheTicketIsOnBase(t *testing.T) {
	f := newMergeFixture(t, "true")
	f.orch.tickets = labelledA()
	f.git(f.repo, "branch", "wt/k-a") // cut, never committed to
	if s := f.orch.loadUnmerged(); s != nil {
		t.Fatal(s.text)
	}
	if got := f.next(t, nil); got != "" {
		t.Errorf("next = %q, want k-b held: wt/k-a is on main but holds no commit naming k-a", got)
	}

	f = newMergeFixture(t, "true")
	f.orch.tickets = labelledA()
	f.onMain(t, "a.txt", "a\n")
	f.git(f.repo, "commit", "-q", "--amend", "-m", "k-a: add a.txt") // merged by hand, branch deleted
	if s := f.orch.loadUnmerged(); s != nil {
		t.Fatal(s.text)
	}
	if got := f.next(t, nil); got != "k-b" {
		t.Errorf("next = %q, want k-b: k-a is on main\n%s", got, f.sink.text())
	}
}

// closedUnreadable is Beads that can't list closed tickets.
type closedUnreadable struct{ readyTickets }

func (closedUnreadable) Closed(label string) ([]Ticket, error) { return nil, errBd }

func TestRunStopsWhenTheUnmergedTicketsCannotBeListed(t *testing.T) {
	log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	o := New(Config{Repo: "repo", Base: "main", Limit: 10, Concurrency: 1}, log, "",
		Deps{Tickets: closedUnreadable{readyTickets{{ID: "A"}}}, Checkout: cleanCheckout{}})
	o.SetSink(&recordSink{})
	if code := o.Run(context.Background()); code != ExitTool {
		t.Errorf("exit code %d, want %d", code, ExitTool)
	}
	if !strings.HasPrefix(o.Final(), "READY_UNREADABLE: could not list the tickets labelled 'unmerged': bd defer A") {
		t.Errorf("final line %q", o.Final())
	}
}

func TestLabelFailureIsWarned(t *testing.T) {
	f := newMergeFixture(t, "true")
	f.orch.notes = brokenBd(nil)
	f.orch.tickets = aBlocksB()
	wt := filepath.Join(t.TempDir(), "k-a")
	f.git(f.repo, "worktree", "add", "-q", "-b", "wt/k-a", wt, "main")
	f.orch.finish(context.Background(), "k-a", "wt/k-a", wt, "tab") // CLOSED_WITHOUT_COMMIT
	if ev := f.sink.text(); !strings.Contains(ev, "LABEL_FAILED: bd could not label k-a 'unmerged': bd defer A") {
		t.Errorf("events:\n%s", ev)
	}
	if got := f.next(t, nil); got != "" {
		t.Errorf("next = %q, want k-b held in this run all the same", got)
	}
}
