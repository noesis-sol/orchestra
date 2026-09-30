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

// Deps are the loop's connections to the tracker, the terminal, git and the organs.
type Deps struct {
	Tickets   Tickets
	Notes     Notes
	Advisor   organ.Client
	AdviceCtx context.Context // cancelled when the maintainer skips triage and the report
}
