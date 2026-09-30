package dispatch

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

// Fakes for a run whose workers stop before their agents start.

type readyTickets []Ticket

func (r readyTickets) Ready() ([]Ticket, error)            { return r, nil }
func (readyTickets) Show(id string) (Ticket, error)        { return Ticket{ID: id, Status: "open"}, nil }
func (readyTickets) Status(id string) (string, error)      { return "open", nil }
func (readyTickets) Describe(id string) string             { return id }
func (readyTickets) Closed(label string) ([]Ticket, error) { return nil, nil }

type cleanCheckout struct{}

func (cleanCheckout) DirtyTree(dir string) string      { return "" }
func (cleanCheckout) DirtyWorktree(dir string) string  { return "" }
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
func (noAgents) Screen(name, status string) string                     { return "" }
func (noAgents) Prompt(ctx context.Context, name, prompt string) error { return nil }
func (noAgents) SendKeys(name string, keys ...string) error            { return nil }
func (noAgents) WaitStarted(ctx context.Context, name string) bool     { return true }

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

type quietHistory struct{}

func (quietHistory) ShortStatus(worktree string) string { return "" }
func (quietHistory) OneLineLog(dir, revs string) string { return "" }
func (quietHistory) DiffStat(worktree string) string    { return "" }
func (quietHistory) Subjects(repo, revs string) string  { return "" }

// promptAgents take their prompt (or refuse it, with promptErr) and are gone once they have.
type promptAgents struct{ promptErr error }

func (promptAgents) Status(name string) (string, error) { return "gone", nil }
func (promptAgents) Screen(name, status string) string  { return "" }
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

func TestShortDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		2 * time.Hour: "2h", 90 * time.Minute: "1h30m", 45 * time.Minute: "45m", 30 * time.Second: "30s",
		50 * time.Millisecond: "50ms", time.Hour + 30*time.Second: "1h0m30s",
	} {
		if got := ShortDuration(d); got != want {
			t.Errorf("ShortDuration(%s) = %q, want %q", d, got, want)
		}
	}
}
