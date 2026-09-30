package dispatch

import (
	"context"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// Tickets is what the loop reads from the tracker (Beads).
type Tickets interface {
	Ready() ([]Ticket, error)              // open, ready tickets, highest priority first
	Show(id string) (Ticket, error)        // with dependencies; Status "unknown" and the cause if unreadable
	Status(id string) (string, error)      // "unknown" and the cause if unreadable
	Describe(id string) string             // as a person reads it, for the organs' evidence
	Closed(label string) ([]Ticket, error) // closed tickets carrying the label
}

// Notes is what the loop writes to the tracker.
type Notes interface {
	AppendNotes(id, note string) error
	Defer(id, reason string) error
	Reopen(id string) error
	AddLabel(id, label string) error
	RemoveLabel(id, label string) error
	SetMetadata(id, key, value string) error
}

// Tabs opens and closes the terminal tabs workers run in (Herdr).
type Tabs interface {
	CreateTab(workspace, cwd, label string) (tab, pane string, err error)
	CloseTab(tab string)
}

// Starter starts a worker agent in a tab's pane.
type Starter interface {
	LaunchInPane(pane, kind string, args []string) error                          // type the command, return at once
	StartAgent(ctx context.Context, name, kind, pane string, args []string) error // start and wait until it looks ready
	IsArgumentRefused(err error) bool                                             // StartAgent can't pass these arguments
	IsNameRefused(err error) bool                                                 // Herdr won't take this agent name
	WaitReady(ctx context.Context, name string) bool
}

// Namer finds and names agents, which is how the loop refers to a worker (by a name derived from
// its ticket).
type Namer interface {
	AgentName(id string) string                                              // the agent name for ticket id's worker
	AdoptAgent(ctx context.Context, pane, kind, name string) (string, error) // name the agent that appears in the pane
	PaneAgent(pane string) (name, kind, status string)                       // status as Agents.Status gives it
	RenameAgent(name, to string) error
	FreeName(name string) string // an unused name for an earlier worker that holds name
}

// Agents watches and nudges a running worker by name.
type Agents interface {
	// Status is idle, working, blocked, done, unknown, or gone (no such agent); if the terminal
	// cannot be asked it is "unreadable", with the error, and says nothing about the agent.
	Status(name string) (string, error)
	// Screen is the end of the worker's terminal, given its status as just read ("" if not known):
	// a working or blocked worker's visible screen is read at once, as its scrollback can't be.
	Screen(name, status string) string
	Prompt(ctx context.Context, name, prompt string) error
	SendKeys(name string, keys ...string) error
	WaitStarted(ctx context.Context, name string) bool
}

// Reporter has a worker report each tool it uses (Claude Code hooks), so the dashboard can say
// what it is doing without reading its screen.
type Reporter interface {
	ReportArgs(worktree string) ([]string, error) // agent arguments that turn reporting on
	LastToolUse(worktree string) (ToolUse, bool)  // false when the worker reported nothing
	EditedFiles(worktree string) []string         // repository files the worker has edited so far
}

// Checkout is what the loop checks about the main checkout and worktrees (git).
type Checkout interface {
	DirtyTree(dir string) (string, error)      // uncommitted work in the main checkout outside .claude/, .beads/, .orchestra/
	DirtyWorktree(dir string) string           // uncommitted work in a ticket's worktree outside .orchestra/run/
	CurrentBranch(repo string) (string, error) // "" on a detached HEAD
	Head(repo, rev string) string
	TrackedFiles(repo string) []string // git ls-files; nil when git can't list them
}

// Worktrees manages the per-ticket worktrees and their branches.
type Worktrees interface {
	WorktreeOf(repo, branch string) string
	HasBranch(repo, branch string) bool
	Prune(repo string)
	AddWorktree(repo, path, branch string) (string, error)
	NewWorktree(repo, path, branch, base string) (string, error)
	RemoveWorktree(repo, path string) (string, error)
	DeleteBranch(repo, branch string) (string, error)
}

// Merger brings finished branches onto the base branch.
type Merger interface {
	IsAncestor(repo, ancestor, rev string) bool
	CommitNaming(repo, base, branch, ticket string) string
	CommitNamingOn(repo, rev, ticket string) string // the latest commit reachable from rev naming the ticket
	Rebase(worktree, onto string) (string, error)
	AbortRebase(worktree string)
	FastForward(repo, branch string) (string, error)
}

// History is what the organs read about the work.
type History interface {
	ShortStatus(worktree string) string
	OneLineLog(dir, revs string) string
	DiffStat(worktree string) string
	Subjects(repo, revs string) string
}

// Deps are the loop's connections to the tracker, the terminal, git and the organs.
type Deps struct {
	Tickets   Tickets
	Notes     Notes
	Tabs      Tabs
	Starter   Starter
	Namer     Namer
	Agents    Agents
	Reporter  Reporter // nil: no reports
	Checkout  Checkout
	Worktrees Worktrees
	Merger    Merger
	History   History
	Advisor   organ.Client
	AdviceCtx context.Context // cancelled when the maintainer skips triage and the report
}
