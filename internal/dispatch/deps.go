package dispatch

import (
	"context"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// Tickets is what the loop reads from the tracker (Beads).
type Tickets interface {
	Ready(ctx context.Context, scope string) ([]Ticket, error)    // open, ready tickets, highest priority first; with a scope, only it and its descendants
	Unclosed(ctx context.Context) ([]Ticket, error)               // every ticket not closed, with its parent
	Descendants(ctx context.Context, id string) ([]Ticket, error) // the ticket's subtickets at any depth, closed or not
	Show(ctx context.Context, id string) (Ticket, error)          // with dependencies; Status "unknown" and the cause if unreadable
	Status(ctx context.Context, id string) (string, error)        // "unknown" and the cause if unreadable
	Describe(ctx context.Context, id string) string               // as a person reads it, for the organs' evidence
	Closed(ctx context.Context, label string) ([]Ticket, error)   // closed tickets carrying the label
}

// Notes is what the loop writes to the tracker.
type Notes interface {
	AppendNotes(ctx context.Context, id, note string) error
	Defer(ctx context.Context, id, reason string) error
	Reopen(ctx context.Context, id string) error
	AddLabel(ctx context.Context, id, label string) error
	RemoveLabel(ctx context.Context, id, label string) error
	SetMetadata(ctx context.Context, id, key, value string) error
}

// Tabs opens and closes the terminal tabs workers run in (Herdr).
type Tabs interface {
	CreateTab(ctx context.Context, workspace, cwd, label string) (tab, pane string, err error)
	CloseTab(ctx context.Context, tab string) error
}

// Starter starts a worker agent in a tab's pane.
type Starter interface {
	LaunchInPane(ctx context.Context, pane, kind string, args []string) error     // type the command, return at once
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
	PaneAgent(ctx context.Context, pane string) (name, kind, status string)  // status as Agents.Status gives it
	RenameAgent(ctx context.Context, name, to string) error
	FreeName(ctx context.Context, name string) string // an unused name for an earlier worker that holds name
}

// Agents watches and nudges a running worker by name.
type Agents interface {
	// Status is idle, working, blocked, done, unknown, or gone (no such agent); if the terminal
	// cannot be asked it is "unreadable", with the error, and says nothing about the agent.
	Status(ctx context.Context, name string) (string, error)
	// Screen is the end of the worker's terminal, given its status as just read ("" if not known):
	// a working or blocked worker's visible screen is read at once, as its scrollback can't be.
	Screen(ctx context.Context, name, status string) string
	Prompt(ctx context.Context, name, prompt string) error
	SendKeys(ctx context.Context, name string, keys ...string) error
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
	DirtyTree(ctx context.Context, dir string) (string, error)      // uncommitted work in the main checkout outside .claude/, .beads/, .orchestra/
	DirtyWorktree(ctx context.Context, dir string) string           // uncommitted work in a ticket's worktree outside .orchestra/run/
	CurrentBranch(ctx context.Context, repo string) (string, error) // "" on a detached HEAD
	Head(ctx context.Context, repo, rev string) string
	TrackedFiles(ctx context.Context, repo string) []string // git ls-files; nil when git can't list them
}

// Worktrees manages the per-ticket worktrees and their branches.
type Worktrees interface {
	WorktreeOf(ctx context.Context, repo, branch string) string
	HasBranch(ctx context.Context, repo, branch string) bool
	Prune(ctx context.Context, repo string) (string, error)
	AddWorktree(ctx context.Context, repo, path, branch string) (string, error)
	NewWorktree(ctx context.Context, repo, path, branch, base string) (string, error)
	RemoveWorktree(ctx context.Context, repo, path string) (string, error)
	DeleteBranch(ctx context.Context, repo, branch string) (string, error)
}

// Merger brings finished branches onto the base branch.
type Merger interface {
	IsAncestor(ctx context.Context, repo, ancestor, rev string) bool
	CommitNaming(ctx context.Context, repo, base, branch, ticket string) string
	CommitNamingOn(ctx context.Context, repo, rev, ticket string) string // the latest commit reachable from rev naming the ticket
	Rebase(ctx context.Context, worktree, onto string) (string, error)
	AbortRebase(ctx context.Context, worktree string) (string, error)
	ConflictedFiles(ctx context.Context, worktree string) []string // files a stopped rebase left unmerged
	RebaseInProgress(ctx context.Context, worktree string) bool    // a rebase stopped and neither finished nor aborted
	CountCommits(ctx context.Context, repo, revs string) int       // -1 if git can't count them
	ResetBranch(ctx context.Context, worktree, rev string) (string, error)
	FastForward(ctx context.Context, repo, branch string) (string, error)
}

// History is what the organs read about the work.
type History interface {
	ShortStatus(ctx context.Context, worktree string) string
	OneLineLog(ctx context.Context, dir, revs string) string
	DiffStat(ctx context.Context, worktree string) string
	Subjects(ctx context.Context, repo, revs string) string
}

// Deps are the loop's connections to the tracker, the terminal, git and the organs. A method that
// runs a command takes a context, which stops it: the run's, cancelled by Ctrl+C, or one detached
// from it for what must finish once begun. The adapters give every command a time limit as well.
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
