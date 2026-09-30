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
	LaunchInPane(pane, kind, instruction string) error                     // type the command, return at once
	StartAgent(ctx context.Context, name, kind, pane, prompt string) error // start and wait until it looks ready
	IsArgumentRefused(err error) bool                                      // StartAgent can't pass these arguments
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

// Deps are the loop's connections to the tracker, the terminal, git and the organs.
type Deps struct {
	Tickets   Tickets
	Notes     Notes
	Tabs      Tabs
	Starter   Starter
	Namer     Namer
	Agents    Agents
	Advisor   organ.Client
	AdviceCtx context.Context // cancelled when the maintainer skips triage and the report
}
