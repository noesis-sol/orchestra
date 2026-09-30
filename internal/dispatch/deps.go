package dispatch

import (
	"context"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// Tickets is what the loop reads from the tracker (Beads).
type Tickets interface {
	Ready() ([]Ticket, error)  // open, ready tickets, highest priority first
	Show(id string) Ticket     // with dependencies; Status "unknown" if unreadable
	Status(id string) string   // "unknown" if unreadable
	Describe(id string) string // as a person reads it, for the organs' evidence
}

// Notes is what the loop writes to the tracker.
type Notes interface {
	AppendNotes(id, note string)
	Defer(id, reason string)
	Reopen(id string)
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
	WaitReady(ctx context.Context, name string) bool
}

// Namer finds and names agents, which is how the loop refers to a worker (by its ticket).
type Namer interface {
	AdoptAgent(ctx context.Context, pane, kind, name string) (string, bool) // name the agent that appears in the pane
	PaneAgent(pane string) (name, kind, status string)
	RenameAgent(name, to string) error
	FreeName(id string) string // an unused name for an earlier worker of ticket id
}

// Agents watches and nudges a running worker by name.
type Agents interface {
	Status(name string) string // idle, working, blocked, done, unknown, or gone
	Screen(name string) string
	Prompt(ctx context.Context, name, prompt string) error
	SendKeys(name string, keys ...string) error
	WaitStarted(ctx context.Context, name string) bool
}

// Reporter has a worker report each tool it uses (Claude Code hooks), so the dashboard can say
// what it is doing without reading its screen.
type Reporter interface {
	ReportArgs(worktree string) ([]string, error) // agent arguments that turn reporting on
	LastToolUse(worktree string) (ToolUse, bool)  // false when the worker reported nothing
}

// Checkout is what the loop checks about the main checkout and worktrees (git).
type Checkout interface {
	DirtyTree(dir string) string // uncommitted work outside .claude/, .beads/, .orchestra/
	CurrentBranch(repo string) string
	Head(repo, rev string) string
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
