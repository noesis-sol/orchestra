// Package beads is orchestra's adapter for Beads (the bd command): the ready queue, a ticket's
// status and dependencies, and the notes and deferrals orchestra writes back.
package beads

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

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
func parseReady(raw []byte) ([]dispatch.Ticket, error) {
	var all []dispatch.Ticket
	if err := json.Unmarshal(unwrap(raw), &all); err != nil {
		return nil, err
	}
	var open []dispatch.Ticket
	for _, t := range all {
		if t.Status == "open" && !dispatch.HasLabel(t, dispatch.HumanLabel) { // questions are for the maintainer
			open = append(open, t)
		}
	}
	prio := func(t dispatch.Ticket) int {
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
	var list []dispatch.Ticket
	if json.Unmarshal(data, &list) == nil {
		if len(list) > 0 && list[0].Status != "" {
			return list[0].Status
		}
		return "unknown"
	}
	var t dispatch.Ticket
	if json.Unmarshal(data, &t) == nil && t.Status != "" {
		return t.Status
	}
	return "unknown"
}

// Tracker is Beads for one repository, as the loop uses it.
type Tracker struct {
	Repo string
}

// Ready returns the open tickets bd considers ready, highest priority first; questions for the
// maintainer are left out.
func (b Tracker) Ready() ([]dispatch.Ticket, error) {
	out, _ := command.Output(b.Repo, "bd", "ready", "--json") // like the bash version, judge by the output
	return parseReady([]byte(out))
}

// parseTicket reads one ticket from 'bd show --json'; ok is false if it cannot be read.
func parseTicket(raw []byte) (dispatch.Ticket, bool) {
	data := unwrap(raw)
	var list []dispatch.Ticket
	if json.Unmarshal(data, &list) == nil {
		if len(list) > 0 && list[0].Status != "" {
			return list[0], true
		}
		return dispatch.Ticket{}, false
	}
	var t dispatch.Ticket
	if json.Unmarshal(data, &t) == nil && t.Status != "" {
		return t, true
	}
	return dispatch.Ticket{}, false
}

// Show returns the ticket with its dependencies; Status is "unknown" if it cannot be read.
func (b Tracker) Show(id string) dispatch.Ticket {
	out, _ := command.Output(b.Repo, "bd", "show", id, "--json")
	t, ok := parseTicket([]byte(out))
	if !ok {
		return dispatch.Ticket{ID: id, Status: "unknown"}
	}
	return t
}

// Status returns the ticket's status, or "unknown" if it cannot be read.
func (b Tracker) Status(id string) string {
	out, _ := command.Output(b.Repo, "bd", "show", id, "--json")
	return parseStatus([]byte(out))
}

// AppendNotes adds a note to the ticket.
func (b Tracker) AppendNotes(id, note string) {
	command.Output(b.Repo, "bd", "update", id, "--append-notes", note)
}

// Defer sets the ticket aside, with the reason.
func (b Tracker) Defer(id, reason string) {
	command.Output(b.Repo, "bd", "defer", id, "--reason="+reason)
}

// Describe returns 'bd show' for the ticket, as a person reads it.
func (b Tracker) Describe(id string) string {
	out, _ := command.Output(b.Repo, "bd", "show", id)
	return out
}

// Reopen puts the ticket back in the queue.
func (b Tracker) Reopen(id string) {
	command.Output(b.Repo, "bd", "update", id, "--status", "open")
}
