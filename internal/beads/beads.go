// Package beads is orchestra's adapter for Beads (the bd command): the ready queue, a ticket's
// status and dependencies, and the notes and deferrals orchestra writes back.
package beads

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/noesis-sol/orchestra/internal/command"
)

// Ticket is a Beads issue as bd prints it with --json.
type Ticket struct {
	ID           string   `json:"id"`
	Title        string   `json:"title"`
	Status       string   `json:"status"`
	Priority     *int     `json:"priority"`
	Labels       []string `json:"labels"`
	Dependencies []Ticket `json:"dependencies"` // from bd show; each carries its status and labels
}

// HumanLabel marks a question for the maintainer (bd human list / respond). Workers ask one as
// its own ticket that blocks theirs; the orchestrator never dispatches it.
const HumanLabel = "human"

// HasLabel reports whether the ticket carries the label.
func HasLabel(t Ticket, label string) bool {
	for _, l := range t.Labels {
		if l == label {
			return true
		}
	}
	return false
}

// OpenQuestion returns the unanswered question the ticket waits on, if any.
func OpenQuestion(t Ticket) *Ticket {
	for i, d := range t.Dependencies {
		if d.Status != "closed" && HasLabel(d, HumanLabel) {
			return &t.Dependencies[i]
		}
	}
	return nil
}

// unwrap accepts either bd JSON shape: the bare payload, or the v2 envelope {schema_version, data}.
func unwrap(raw []byte) []byte {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '{' {
		var env struct {
			Data json.RawMessage `json:"data"`
		}
		if json.Unmarshal(raw, &env) == nil && env.Data != nil {
			return env.Data
		}
	}
	return raw
}

// parseReady returns the open tickets from 'bd ready --json', highest priority (lowest number) first.
func parseReady(raw []byte) ([]Ticket, error) {
	var all []Ticket
	if err := json.Unmarshal(unwrap(raw), &all); err != nil {
		return nil, err
	}
	var open []Ticket
	for _, t := range all {
		if t.Status == "open" && !HasLabel(t, HumanLabel) { // questions are for the maintainer
			open = append(open, t)
		}
	}
	prio := func(t Ticket) int {
		if t.Priority == nil {
			return 9
		}
		return *t.Priority
	}
	sort.SliceStable(open, func(i, j int) bool { return prio(open[i]) < prio(open[j]) })
	return open, nil
}

// parseStatus returns the status from 'bd show --json', or "unknown" if it cannot be read.
func parseStatus(raw []byte) string {
	data := unwrap(raw)
	var list []Ticket
	if json.Unmarshal(data, &list) == nil {
		if len(list) > 0 && list[0].Status != "" {
			return list[0].Status
		}
		return "unknown"
	}
	var t Ticket
	if json.Unmarshal(data, &t) == nil && t.Status != "" {
		return t.Status
	}
	return "unknown"
}

// Ready returns the open tickets bd considers ready, highest priority first.
func Ready(repo string) ([]Ticket, error) {
	out, _ := command.Output(repo, "bd", "ready", "--json") // like the bash version, judge by the output
	return parseReady([]byte(out))
}

// parseTicket reads one ticket from 'bd show --json'; ok is false if it cannot be read.
func parseTicket(raw []byte) (Ticket, bool) {
	data := unwrap(raw)
	var list []Ticket
	if json.Unmarshal(data, &list) == nil {
		if len(list) > 0 && list[0].Status != "" {
			return list[0], true
		}
		return Ticket{}, false
	}
	var t Ticket
	if json.Unmarshal(data, &t) == nil && t.Status != "" {
		return t, true
	}
	return Ticket{}, false
}

// Show returns the ticket with its dependencies; Status is "unknown" if it cannot be read.
func Show(repo, id string) Ticket {
	out, _ := command.Output(repo, "bd", "show", id, "--json")
	t, ok := parseTicket([]byte(out))
	if !ok {
		return Ticket{ID: id, Status: "unknown"}
	}
	return t
}

// Status returns the ticket's status, or "unknown" if it cannot be read.
func Status(repo, id string) string {
	out, _ := command.Output(repo, "bd", "show", id, "--json")
	return parseStatus([]byte(out))
}

// AppendNotes adds a note to the ticket.
func AppendNotes(repo, id, note string) {
	command.Output(repo, "bd", "update", id, "--append-notes", note)
}

// Defer sets the ticket aside, with the reason.
func Defer(repo, id, reason string) {
	command.Output(repo, "bd", "defer", id, "--reason="+reason)
}
