// Package tracker is the vocabulary the Beads backlog is read in, shared by the run loop
// (internal/dispatch) and the adapter that reads it (internal/beads): a ticket as the loop sees it, its
// statuses, the links between tickets, the labels and metadata the loop gives meaning to, and the
// error of a ticket the tracker has no trace of. It imports none of orchestra's packages, so the
// adapter depends on it rather than on the run loop.
package tracker

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

// Ticket is a Beads issue as the loop sees it: status, priority, labels and what it depends on.
type Ticket struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Status       Status   `json:"status"`
	IssueType    string   `json:"issue_type"` // task, bug, feature, epic, …
	Priority     *int     `json:"priority"`
	CreatedAt    string   `json:"created_at"` // RFC 3339, so older sorts first
	Labels       []string `json:"labels"`
	Dependencies []Ticket `json:"dependencies"` // from bd show; each carries its status and labels
	// DependencyType is how a dependency links to the ticket (blocks, related, parent-child,
	// discovered-from); set only on the entries of Dependencies.
	DependencyType string `json:"dependency_type"`
	// DependencyCount is how many tickets block this one, closed or not, as bd ready counts them
	// (bd show counts every link); nil when bd doesn't say. It changes when a blocks link is added
	// or removed.
	DependencyCount *int `json:"dependency_count"`
	// DependentCount is how many tickets this one blocks, closed or not, as bd ready counts them; nil
	// when bd doesn't say.
	DependentCount *int `json:"dependent_count"`
	// Parent is the ticket this one is a subticket of (a parent-child link), or "".
	Parent string `json:"parent"`
	// DeferUntil is when a ticket deferred until a date (bd defer --until) is due again, RFC 3339;
	// "" when it has none. See DeferredUntilLater.
	DeferUntil string `json:"defer_until"`

	// The ticket's text and custom metadata. All but its notes and close reason, which say what happened
	// to it, say where it works (the loop's Footprint).
	Description        string          `json:"description"`
	Design             string          `json:"design"`
	AcceptanceCriteria string          `json:"acceptance_criteria"`
	Notes              string          `json:"notes"`
	CloseReason        string          `json:"close_reason"` // what the worker said as it closed the ticket
	Metadata           json.RawMessage `json:"metadata"`     // an object, or one encoded as a string
}

// DeferredUntilLater reports whether the ticket was deferred until a date after now: deferred on
// purpose, with a time to look at it again. A date already past, or one that can't be read, counts
// as none.
func (t Ticket) DeferredUntilLater(now time.Time) bool {
	if t.DeferUntil == "" {
		return false
	}
	until, err := time.Parse(time.RFC3339, t.DeferUntil)
	return err == nil && until.After(now)
}

// MetadataField returns the value the ticket's metadata holds at key, or nil when it holds none or
// isn't an object: bd gives the metadata as an object or as one encoded in a string.
func (t Ticket) MetadataField(key string) json.RawMessage {
	raw := t.Metadata
	var s string
	if json.Unmarshal(raw, &s) == nil {
		raw = json.RawMessage(s) // an object encoded as a string
	}
	var meta map[string]json.RawMessage
	if json.Unmarshal(raw, &meta) != nil {
		return nil
	}
	return meta[key]
}

// MetadataText is the text the ticket's metadata gives key, trimmed: "" when it gives none, or a
// value that isn't text.
func (t Ticket) MetadataText(key string) string {
	var s string
	if json.Unmarshal(t.MetadataField(key), &s) != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

// Status is a ticket's status as Beads stores it, or StatusUnknown when bd can't say.
type Status string

// The statuses a ticket can have.
const (
	StatusOpen       Status = "open"
	StatusInProgress Status = "in_progress"
	StatusBlocked    Status = "blocked"
	StatusDeferred   Status = "deferred"
	StatusClosed     Status = "closed"
	StatusUnknown    Status = "unknown" // bd could not show the ticket; not a status Beads has
)

// ErrNoTicket is, through errors.Is, the error of a read of a ticket the tracker has no trace of, as
// one deleted from it, rather than of the tracker failing to say.
var ErrNoTicket = errors.New("no such ticket")

// Link is a blocks link between two tickets: Blocked waits for Blocker (bd dep add <Blocked>
// <Blocker>). Why is what both touch, for a proposed link.
type Link struct {
	Blocker, Blocked string
	Why              string
}

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

// FilesKey is the metadata key listing the files a ticket works on, as a list, a list in a string
// or a string of paths separated by commas or spaces (bd update <id> --set-metadata files=a.go,b.go).
const FilesKey = "files"
