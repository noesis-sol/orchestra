package dispatch

import "github.com/noesis-sol/orchestra/internal/tracker"

// Ticket is a Beads issue as the loop sees it: status, priority, labels and what it depends on
// (tracker.Ticket).
type Ticket = tracker.Ticket

// TicketStatus is a ticket's status as Beads stores it, or StatusUnknown when bd can't say
// (tracker.Status).
type TicketStatus = tracker.Status

// The statuses a ticket can have (tracker's).
const (
	StatusOpen       = tracker.StatusOpen
	StatusInProgress = tracker.StatusInProgress
	StatusBlocked    = tracker.StatusBlocked
	StatusDeferred   = tracker.StatusDeferred
	StatusClosed     = tracker.StatusClosed
	StatusUnknown    = tracker.StatusUnknown
)

// ErrNoTicket is, through errors.Is, the error of a Tickets read of a ticket the tracker has no
// trace of, as one deleted from it, rather than of the tracker failing to say (tracker.ErrNoTicket).
var ErrNoTicket = tracker.ErrNoTicket

// HumanLabel marks a question for the maintainer (tracker.HumanLabel): the orchestrator never
// dispatches it.
const HumanLabel = tracker.HumanLabel

// HasLabel reports whether the ticket carries the label (tracker.HasLabel).
func HasLabel(t Ticket, label string) bool { return tracker.HasLabel(t, label) }

// OpenQuestion returns the unanswered question the ticket waits on, if any (tracker.OpenQuestion).
func OpenQuestion(t Ticket) *Ticket { return tracker.OpenQuestion(t) }

// UnmergedLabel marks a ticket closed but left unmerged, so later runs still hold the tickets it
// blocks (tracker.UnmergedLabel).
const UnmergedLabel = tracker.UnmergedLabel

// SoloLabel marks a ticket that runs alone (tracker.SoloLabel).
const SoloLabel = tracker.SoloLabel
