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

func (noTabs) CreateTab(ctx context.Context, workspace, cwd, label string) (string, string, error) {
	return "tab", "pane", nil
}
func (noTabs) CloseTab(ctx context.Context, tab string) error { return nil }

// Fakes for a run whose workers stop before their agents start.

type readyTickets []Ticket

func (r readyTickets) Ready(context.Context, string) ([]Ticket, error)     { return r, nil }
func (readyTickets) Unclosed(context.Context) ([]Ticket, error)            { return nil, nil }
func (readyTickets) Descendants(context.Context, string) ([]Ticket, error) { return nil, nil }
func (readyTickets) Show(ctx context.Context, id string) (Ticket, error) {
	return Ticket{ID: id, Status: "open"}, nil
}
func (readyTickets) Status(ctx context.Context, id string) (string, error)      { return "open", nil }
func (readyTickets) Describe(ctx context.Context, id string) string             { return id }
func (readyTickets) Closed(ctx context.Context, label string) ([]Ticket, error) { return nil, nil }

type cleanCheckout struct{}

func (cleanCheckout) DirtyTree(ctx context.Context, dir string) (string, error) { return "", nil }
func (cleanCheckout) DirtyWorktree(ctx context.Context, dir string) string      { return "" }
func (cleanCheckout) CurrentBranch(ctx context.Context, repo string) (string, error) {
	return "main", nil
}
func (cleanCheckout) Head(ctx context.Context, repo, rev string) string      { return "abc" }
func (cleanCheckout) TrackedFiles(ctx context.Context, repo string) []string { return nil }

// newWorktrees creates every worktree.
type newWorktrees struct{}

func (newWorktrees) WorktreeOf(ctx context.Context, repo, branch string) string { return "" }
func (newWorktrees) HasBranch(ctx context.Context, repo, branch string) bool    { return false }
func (newWorktrees) Prune(ctx context.Context, repo string) (string, error)     { return "", nil }
func (newWorktrees) AddWorktree(ctx context.Context, repo, path, branch string) (string, error) {
	return "", nil
}
func (newWorktrees) NewWorktree(ctx context.Context, repo, path, branch, base string) (string, error) {
	return "", nil
}
func (newWorktrees) RemoveWorktree(ctx context.Context, repo, path string) (string, error) {
	return "", nil
}
func (newWorktrees) DeleteBranch(ctx context.Context, repo, branch string) (string, error) {
	return "", nil
}

type upToDate struct{}

func (upToDate) IsAncestor(ctx context.Context, repo, ancestor, rev string) bool { return true }
func (upToDate) CommitNaming(ctx context.Context, repo, base, branch, ticket string) string {
	return ""
}
func (upToDate) CommitNamingOn(ctx context.Context, repo, rev, ticket string) string { return "" }
func (upToDate) Rebase(ctx context.Context, worktree, onto string) (string, error)   { return "", nil }
func (upToDate) AbortRebase(ctx context.Context, worktree string) (string, error)    { return "", nil }
func (upToDate) ConflictedFiles(ctx context.Context, worktree string) []string       { return nil }
func (upToDate) RebaseInProgress(ctx context.Context, worktree string) bool          { return false }
func (upToDate) CountCommits(ctx context.Context, repo, revs string) int             { return 0 }
func (upToDate) ResetBranch(ctx context.Context, worktree, rev string) (string, error) {
	return "", nil
}
func (upToDate) FastForward(ctx context.Context, repo, branch string) (string, error) { return "", nil }

type noAgents struct{}

func (noAgents) Status(ctx context.Context, name string) (string, error)         { return "gone", nil }
func (noAgents) Screen(ctx context.Context, name, status string) string          { return "" }
func (noAgents) Prompt(ctx context.Context, name, prompt string) error           { return nil }
func (noAgents) SendKeys(ctx context.Context, name string, keys ...string) error { return nil }
func (noAgents) WaitStarted(ctx context.Context, name string) bool               { return true }

// Fakes for a run whose workers start, against a bd that fails.

// errBd is what the fakes' bd says when it fails.
var errBd = fmt.Errorf("bd defer A: exit status 1: Error: database is locked\n  (another bd holds it)")

// brokenBd lists its tickets as ready but can't show their status, defer, note or reopen them.
type brokenBd []Ticket

func (b brokenBd) Ready(context.Context, string) ([]Ticket, error)     { return b, nil }
func (brokenBd) Unclosed(context.Context) ([]Ticket, error)            { return nil, nil }
func (brokenBd) Descendants(context.Context, string) ([]Ticket, error) { return nil, nil }
func (brokenBd) Show(ctx context.Context, id string) (Ticket, error) {
	return Ticket{ID: id, Status: "unknown"}, errBd
}
func (brokenBd) Status(ctx context.Context, id string) (string, error)        { return "unknown", errBd }
func (brokenBd) Describe(ctx context.Context, id string) string               { return id }
func (brokenBd) AppendNotes(ctx context.Context, id, note string) error       { return errBd }
func (brokenBd) Defer(ctx context.Context, id, reason string) error           { return errBd }
func (brokenBd) Reopen(ctx context.Context, id string) error                  { return errBd }
func (brokenBd) AddLabel(ctx context.Context, id, label string) error         { return errBd }
func (brokenBd) RemoveLabel(ctx context.Context, id, label string) error      { return errBd }
func (brokenBd) SetMetadata(ctx context.Context, id, key, value string) error { return errBd }
func (brokenBd) Closed(ctx context.Context, label string) ([]Ticket, error)   { return nil, nil } // so the run gets as far as the workers

type okTabs struct{}

func (okTabs) CreateTab(ctx context.Context, workspace, cwd, label string) (string, string, error) {
	return "tab-" + label, "pane-" + label, nil
}
func (okTabs) CloseTab(ctx context.Context, tab string) error { return nil }

// Fakes for a run whose worker defers its ticket; the loop blocks gathering the evidence for triage
// (Describe) until release is closed.

type okStarter struct{}

func (okStarter) LaunchInPane(ctx context.Context, pane, kind string, args []string) error {
	return nil
}
func (okStarter) StartAgent(ctx context.Context, name, kind, pane string, args []string) error {
	return nil
}
func (okStarter) IsArgumentRefused(err error) bool                { return false }
func (okStarter) IsNameRefused(err error) bool                    { return false }
func (okStarter) WaitReady(ctx context.Context, name string) bool { return true }

type quietHistory struct{}

func (quietHistory) ShortStatus(ctx context.Context, worktree string) string { return "" }
func (quietHistory) OneLineLog(ctx context.Context, dir, revs string) string { return "" }
func (quietHistory) DiffStat(ctx context.Context, worktree string) string    { return "" }
func (quietHistory) Subjects(ctx context.Context, repo, revs string) string  { return "" }

// promptAgents take their prompt (or refuse it, with promptErr) and are gone once they have.
type promptAgents struct{ promptErr error }

func (promptAgents) Status(ctx context.Context, name string) (string, error) { return "gone", nil }
func (promptAgents) Screen(ctx context.Context, name, status string) string  { return "" }
func (a promptAgents) Prompt(ctx context.Context, name, prompt string) error {
	return a.promptErr
}
func (promptAgents) SendKeys(ctx context.Context, name string, keys ...string) error { return nil }
func (promptAgents) WaitStarted(ctx context.Context, name string) bool               { return false }

func brokenBdRun(t *testing.T, agents Agents) (*Loop, *recordSink, string, int) {
	t.Helper()
	noLeaks(t)
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
