package organ

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
)

// ---- Triage --------------------------------------------------------------------------

// triage asks, for each deferred ticket, where the cause lies: the environment, the instructions or
// the problem itself.
var triage = spec{name: "triage", limit: 3 * time.Minute, effort: "low", schema: triageSchema,
	system: systemPrompt("triage", fromTheRun)}

const triageSchema = `{"type":"object","properties":{"cause":{"type":"string",` +
	`"enum":["environment","instructions","problem"]},"confidence":{"type":"string",` +
	`"enum":["high","medium","low"]},"summary":{"type":"string"},` +
	`"recommendation":{"type":"string"}},"required":["cause","confidence","summary",` +
	`"recommendation"]}`

// Verdict is the triage organ's answer.
type Verdict struct {
	Cause          string `json:"cause"`
	Confidence     string `json:"confidence"`
	Summary        string `json:"summary"`
	Recommendation string `json:"recommendation"`
}

// Deferral is the evidence gathered when a ticket is set aside, while its tab is still open.
type Deferral struct {
	ID, How string // How: who deferred it and why, as logged
	Ticket  string // bd show
	Screen  string // the end of the worker's terminal
	// the end of its session's transcript: its last messages, tool calls and errors; "" if unread
	Transcript string
	Hooks      string // what the worker's hooks noted: permission prompts, compactions; "" if none
	Worktree   string // status, commits and diff stat
}

// triageInput names the ticket by ID only: its title is ticket text, as untrusted as the rest, and
// stays inside the evidence tags with bd show. How is orchestra's own words, and they come last,
// after the evidence, as Claude reads long inputs best with the request at the end. The
// transcript's section is left out when it couldn't be read, the hooks' when they noted nothing.
func triageInput(d Deferral) string {
	id := EvidenceID()
	in := Section(id, "Ticket (bd show)", d.Ticket) +
		Section(id, "End of the worker's terminal", d.Screen)
	if d.Transcript != "" {
		in += Section(id, "End of the worker's session transcript (messages, tool calls, results)", d.Transcript)
	}
	if d.Hooks != "" {
		in += Section(id, "What the worker's hooks noted (permission prompts, compactions)", d.Hooks)
	}
	return in + Section(id, "Worktree state", d.Worktree) +
		"Ticket " + d.ID + " was set aside: " + d.How + "\n"
}

func parseTriage(r Result) (Verdict, error) {
	var t Verdict
	if err := r.decode(&t); err != nil {
		return t, fmt.Errorf("unreadable triage: %w", err)
	}
	switch {
	case !slices.Contains([]string{"environment", "instructions", "problem"}, t.Cause):
		return t, fmt.Errorf("unknown triage cause %q", t.Cause)
	case !slices.Contains([]string{"high", "medium", "low"}, t.Confidence):
		return t, fmt.Errorf("unknown triage confidence %q", t.Confidence)
	}
	return t, nil
}

// Note is what goes into the ticket: advice only, the status is untouched.
func (t Verdict) Note() string {
	return fmt.Sprintf("Triage (orchestra): cause = %s (%s confidence). %s Recommendation: %s",
		t.Cause, t.Confidence, strings.TrimSpace(t.Summary), strings.TrimSpace(t.Recommendation))
}

// Triage asks where the cause of a deferral lies: the environment, the instructions or the problem.
func (g Client) Triage(ctx context.Context, d Deferral) (Verdict, error) {
	return askAs(ctx, g, triage, triageInput(d), parseTriage)
}
