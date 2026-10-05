package dispatch

import (
	"encoding/json"
	"slices"
)

// Ticket is a Beads issue as the loop sees it: status, priority, labels and what it depends on.
type Ticket struct {
	ID           string       `json:"id"`
	Title        string       `json:"title"`
	Status       TicketStatus `json:"status"`
	IssueType    string       `json:"issue_type"` // task, bug, feature, epic, …
	Priority     *int         `json:"priority"`
	CreatedAt    string       `json:"created_at"` // RFC 3339, so older sorts first
	Labels       []string     `json:"labels"`
	Dependencies []Ticket     `json:"dependencies"` // from bd show; each carries its status and labels
	// DependencyType is how a dependency links to the ticket (blocks, related, parent-child,
	// discovered-from); set only on the entries of Dependencies.
	DependencyType string `json:"dependency_type"`
	// DependencyCount is how many tickets block this one, closed or not, as bd ready counts them
	// (bd show counts every link); nil when bd doesn't say. It changes when a blocks link is added
	// or removed.
	DependencyCount *int `json:"dependency_count"`
	// Parent is the ticket this one is a subticket of (a parent-child link), or "".
	Parent string `json:"parent"`

	// The ticket's text and custom metadata, which say where it works (its Footprint).
	Description        string          `json:"description"`
	Design             string          `json:"design"`
	AcceptanceCriteria string          `json:"acceptance_criteria"`
	Notes              string          `json:"notes"`
	Metadata           json.RawMessage `json:"metadata"` // an object, or one encoded as a string
}

// TicketStatus is a ticket's status as Beads stores it, or StatusUnknown when bd can't say.
type TicketStatus string

// The statuses a ticket can have.
const (
	StatusOpen       TicketStatus = "open"
	StatusInProgress TicketStatus = "in_progress"
	StatusBlocked    TicketStatus = "blocked"
	StatusDeferred   TicketStatus = "deferred"
	StatusClosed     TicketStatus = "closed"
	StatusUnknown    TicketStatus = "unknown" // bd could not show the ticket; not a status Beads has
)

// HumanLabel marks a question for the maintainer (bd human list / respond). Workers ask one as
// its own ticket that blocks theirs; the orchestrator never dispatches it.
const HumanLabel = "human"

// HasLabel reports whether the ticket carries the label.
func HasLabel(t Ticket, label string) bool {
	return slices.Contains(t.Labels, label)
}

// OpenQuestion returns the unanswered question the ticket waits on, if any. Only a blocks link
// counts: a related or parent-child link to a question doesn't hold the ticket out of bd ready.
func OpenQuestion(t Ticket) *Ticket {
	for i, d := range t.Dependencies {
		if d.DependencyType == "blocks" && d.Status != StatusClosed && HasLabel(d, HumanLabel) {
			return &t.Dependencies[i]
		}
	}
	return nil
}

// UnmergedLabel marks a ticket closed but left unmerged (MERGE_CONFLICT, CHECKS_FAILED, …), so later
// runs still hold the tickets it blocks: bd ready counts a closed blocker as done. The loop removes
// it once the ticket merges.
const UnmergedLabel = "unmerged"

// SoloLabel marks a ticket that runs alone, as one restructuring code every other ticket touches
// does: it starts only once nothing else runs, and nothing else starts while it runs.
const SoloLabel = "solo"
