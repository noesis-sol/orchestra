package dispatch

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// Tickets is what the loop reads from the tracker (Beads).
type Tickets interface {
	// open, ready tickets, highest priority first; with a scope, only it and its descendants
	Ready(ctx context.Context, scope string) ([]Ticket, error)
	Unclosed(ctx context.Context) ([]Ticket, error)               // every ticket not closed, with its parent
	Descendants(ctx context.Context, id string) ([]Ticket, error) // the ticket's subtickets at any depth, closed or not
	// with dependencies; Status StatusUnknown and the cause if unreadable, ErrNoTicket (errors.Is) if gone
	Show(ctx context.Context, id string) (Ticket, error)
	Status(ctx context.Context, id string) (TicketStatus, error) // StatusUnknown and the cause if unreadable
	Describe(ctx context.Context, id string) string              // as a person reads it, for the organs' evidence
	Closed(ctx context.Context, label string) ([]Ticket, error)  // closed tickets carrying the label
}

// Notes is what the loop writes to the tracker.
type Notes interface {
	AppendNotes(ctx context.Context, id, note string) error
	Defer(ctx context.Context, id, reason string) error
	Reopen(ctx context.Context, id string) error
	AddLabel(ctx context.Context, id, label string) error
	RemoveLabel(ctx context.Context, id, label string) error
	SetMetadata(ctx context.Context, id, key, value string) error
	FileBug(ctx context.Context, title, description, label string) (string, error) // files a P2 bug; its ID
}

// Tabs opens and closes the terminal tabs workers run in (Herdr's tabs, tmux windows or cmux workspaces).
type Tabs interface {
	CreateTab(ctx context.Context, workspace, cwd, label string) (tab, pane string, err error)
	CloseTab(ctx context.Context, tab string) error
	// the tab's label, as CreateTab gave it unless renamed since; open is false when the terminal has no such tab
	TabLabel(ctx context.Context, tab string) (label string, open bool, err error)
}

// Starter starts a worker agent in a tab's pane.
//
// Neither LaunchInPane nor StartAgent types into a program still running in the pane, which would
// take the command for its input (a Claude Code on its trust dialog, say, for an answer or a
// prompt): each either refuses while one runs there or replaces it. Herdr and cmux refuse; tmux
// replaces it.
//
// What the terminal refuses for good, a name (here or in Namer's AdoptAgent and RenameAgent) or
// StartAgent's arguments, it says with ErrNameRefused or ErrArgumentRefused (errors.Is): a retry with
// them can't succeed.
type Starter interface {
	LaunchInPane(ctx context.Context, pane, kind string, args []string) error     // type the command, return at once
	StartAgent(ctx context.Context, name, kind, pane string, args []string) error // start and wait until it looks ready
	WaitReady(ctx context.Context, name string) bool
}

// ErrNameRefused is the terminal refusing an agent name (errors.Is): it would refuse it on every
// try. Only Herdr has a rule for names; tmux and cmux take any.
var ErrNameRefused = errors.New("the terminal refuses the agent name")

// ErrArgumentRefused is the terminal refusing the agent's arguments (errors.Is), as Herdr does those
// it can't type into a shell: it would refuse them on every try. tmux and cmux pass any.
var ErrArgumentRefused = errors.New("the terminal refuses the agent's arguments")

// How long every terminal waits for an agent: to be ready for a prompt (Starter.StartAgent,
// Starter.WaitReady), to start on a prompt it submits (Agents.Prompt), and to start working on one
// it was given at launch or by a key (Agents.WaitStarted).
const (
	ReadyWithin   = time.Minute
	PromptWithin  = 30 * time.Second
	StartedWithin = 20 * time.Second
)

// Namer finds and names agents, which is how the loop refers to a worker (by a name derived from
// its ticket).
type Namer interface {
	// the agent name for ticket id's worker
	AgentName(id string) string
	// name the agent that appears in the pane
	AdoptAgent(ctx context.Context, pane, kind, name string) (AgentState, error)
	// state and error as Agents.Status gives them
	PaneAgent(ctx context.Context, pane string) (name, kind string, state AgentState, err error)
	// give an agent a new name; agent is its name or its pane's ID
	RenameAgent(ctx context.Context, agent, to string) error
	FreeName(ctx context.Context, name string) string // an unused name for an earlier worker that holds name
}

// AgentState is a worker's status as the terminal reports it, or StateGone when it has no such agent.
type AgentState string

// The states an agent can be in.
const (
	StateIdle    AgentState = "idle"
	StateWorking AgentState = "working"
	StateBlocked AgentState = "blocked"
	StateDone    AgentState = "done"
	StateUnknown AgentState = "unknown" // the terminal can't tell what the agent is doing
	StateGone    AgentState = "gone"    // the terminal has no such agent
)

// Agents watches and nudges a running worker by name.
type Agents interface {
	// Status is the worker's state. An error means the terminal could not be asked, and says
	// nothing about the agent: the state is then "".
	Status(ctx context.Context, name string) (AgentState, error)
	// Screen is the end of the worker's terminal, given its state as just read ("" if not known): its
	// last ScreenLines lines (LastLines), with wrapped lines joined, or only those of its visible
	// screen while it works or is blocked, as its scrollback can't be read then. "" when the terminal
	// can't read it.
	Screen(ctx context.Context, name string, state AgentState) string
	// Prompt submits a prompt and returns once the worker has started on it (working or blocked),
	// not when its turn ends; an error means it was not seen to start.
	Prompt(ctx context.Context, name, prompt string) error
	SendKeys(ctx context.Context, name string, keys ...string) error
	WaitStarted(ctx context.Context, name string) bool
}

// ScreenLines is how many lines of a worker's terminal Agents.Screen gives, at most, in every terminal.
const ScreenLines = 80

// LastLines is the last n lines of s, a terminal's screen, with the empty lines below its last one
// left out, ending in "\n"; "" when it has none.
func LastLines(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n") + "\n"
}

// PromptTyper is Agents whose Prompt can put other text than the prompt in the worker's input box:
// cmux before 0.65 types a line attaching a file that holds a prompt of several lines.
type PromptTyper interface {
	// TypedPrompt is what Prompt puts in the worker's input box for prompt
	TypedPrompt(ctx context.Context, name, prompt string) string
}

// Reporter has a worker report each tool it uses (Claude Code hooks), so the dashboard can say
// what it is doing without reading its screen.
type Reporter interface {
	ReportArgs(worktree string) ([]string, error) // agent arguments that turn reporting on
	ResumeArgs(worktree string) ([]string, error) // the same for a resumed worker, keeping the files edited before
	LastToolUse(worktree string) (ToolUse, bool)  // false when the worker reported nothing
	HookRecord(worktree string) HookRecord        // its permission prompts, compactions and subagents
	EditedFiles(worktree string) []string         // repository files the worker has edited so far
	Session(worktree string) (Session, bool)      // the worker's session, false when there is none to resume
	TranscriptTail(worktree string) string        // the end of its session's transcript, as text; "" if unreadable
	// ReportArgs for a worker with no worktree of its own, its records in the folder of that name in
	// dir's .orchestra/run/ (project.ProbeRun, project.InterviewRun)
	ReportArgsIn(dir, folder string) ([]string, error)
}

// Session is a Claude Code worker's session, as its hooks recorded it: resumed (claude --resume),
// the worker has all it knew of its ticket.
type Session struct {
	ID         string // the session's ID, a UUID
	Transcript string // the path of its transcript
}

// Checkout is what the loop checks about the main checkout and worktrees (git).
type Checkout interface {
	// uncommitted work in the main checkout outside .claude/, .beads/, .orchestra/
	DirtyTree(ctx context.Context, dir string) (string, error)
	// uncommitted work in a ticket's worktree outside .orchestra/run/
	DirtyWorktree(ctx context.Context, dir string) string
	// each changed or untracked file in repo (under paths, if any) to its code in git status --porcelain
	Changes(ctx context.Context, repo string, paths ...string) map[string]string
	CurrentBranch(ctx context.Context, repo string) (string, error) // "" on a detached HEAD
	Head(ctx context.Context, repo, rev string) string
	TrackedFiles(ctx context.Context, repo string) []string // git ls-files; nil when git can't list them
}

// Worktrees manages the per-ticket worktrees and their branches.
type Worktrees interface {
	// the worktree that has branch checked out, or "" for none; the error is git failing to tell
	WorktreeOf(ctx context.Context, repo, branch string) (string, error)
	HasBranch(ctx context.Context, repo, branch string) bool
	Prune(ctx context.Context, repo string) (string, error)
	AddWorktree(ctx context.Context, repo, path, branch string) (string, error)
	NewWorktree(ctx context.Context, repo, path, branch, base string) (string, error)
	DetachedWorktree(ctx context.Context, repo, path, rev string) (string, error) // rev on no branch
	RemoveWorktree(ctx context.Context, repo, path string) (string, error)
	DeleteBranch(ctx context.Context, repo, branch string) (string, error)
}

// Merger brings finished branches onto the base branch.
type Merger interface {
	IsAncestor(ctx context.Context, repo, ancestor, rev string) bool
	MergeBase(ctx context.Context, repo, a, b string) string // their best common ancestor; "" if git can't say
	CommitNaming(ctx context.Context, repo, base, branch, ticket string) string
	// the latest commit reachable from rev naming the ticket
	CommitNamingOn(ctx context.Context, repo, rev, ticket string) string
	// the oldest commit in revs (a..b) whose message doesn't name the ticket; "" when each one does
	CommitNotNaming(ctx context.Context, repo, revs, ticket string) (string, error)
	Rebase(ctx context.Context, worktree, onto string) (string, error)
	AbortRebase(ctx context.Context, worktree string) (string, error)
	ConflictedFiles(ctx context.Context, worktree string) []string // files a stopped rebase left unmerged
	RebaseInProgress(ctx context.Context, worktree string) bool    // a rebase stopped and neither finished nor aborted
	CountCommits(ctx context.Context, repo, revs string) int       // -1 if git can't count them
	// branch's tree is its merge-base's with base: its commits, taken together, change nothing
	Unchanged(ctx context.Context, repo, base, branch string) bool
	// base holds branch's changes: merging it would change nothing, as after a squash merge
	Holds(ctx context.Context, repo, base, branch string) bool
	ResetBranch(ctx context.Context, worktree, rev string) (string, error)
	FastForward(ctx context.Context, repo, branch string) (string, error)
}

// History is what the organs read about the work.
type History interface {
	ShortStatus(ctx context.Context, worktree string) string
	OneLineLog(ctx context.Context, dir, revs string) string
	DiffStat(ctx context.Context, worktree string) string
	Subjects(ctx context.Context, repo, revs string) string
	ChangedFiles(ctx context.Context, repo, from, to string) []string // the files that differ; none if git can't say
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
