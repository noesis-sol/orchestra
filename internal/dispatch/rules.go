package dispatch

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A Claude worker gets its standing rules in its system prompt, and only what is particular to its
// start in its first message. Claude Code sends the system prompt with every request, so the rules
// survive when a long ticket's conversation is compacted, while a summary may lose instructions
// given early in it: never push, the ways to end (DONE, a question, a deferral), checks in the
// foreground. Workers of other kinds get the whole prompt as their first message.

// workerRules are ticket id's worker's standing rules: the worker prompt with its TICKET_ID filled
// in, and what the run adds for the whole of the ticket (its scope, its time limit, the usage limit).
func (o *Loop) workerRules(id string) string {
	return strings.ReplaceAll(o.prompt, "TICKET_ID", id) + o.scopeNote(id) + o.budgetNote() + o.limitNote()
}

// fullPrompt is ticket id's whole prompt as one first message, for a worker without its rules in
// its system prompt: the worker prompt, then first (see firstNotes) and the run's notes.
func (o *Loop) fullPrompt(id, first string) string {
	return strings.ReplaceAll(o.prompt, "TICKET_ID", id) + first + o.scopeNote(id) + o.budgetNote() + o.limitNote()
}

// limitNote tells a Claude worker what to do at the account's usage limit, which Claude Code may let its
// turn run a little past, telling it so (its grace allowance): the run holds for the limit and has the
// worker carry on after the reset, while a ticket deferred or closed for it would be set aside or merged
// unfinished. "" for workers of other kinds.
func (o *Loop) limitNote() string {
	if !o.cfg.ClaudeWorkers() {
		return ""
	}
	return "\n\nAt the account's usage limit, commit what you have and stop, without deferring or closing the " +
		"ticket for it: you'll carry on after the reset.\n"
}

// ticketPrompt is the first message of ticket id's worker, its standing rules in its system prompt:
// its ticket, and first (see firstNotes).
func ticketPrompt(id, first string) string {
	return fmt.Sprintf("Your ticket is %s. Your standing rules for it are in your system prompt "+
		"(a copy is in %s): follow them exactly, from claiming the ticket to closing it.\n",
		id, project.RunPath(project.RulesName)) + first
}

// firstNotes is what a new worker's first message says of its start alone, beside its ticket or its
// whole prompt: the spec ticket t comes from (see specNote), then what an earlier attempt left,
// earlier (see earlierNote).
func firstNotes(t Ticket, earlier string) string {
	if s := specNote(t, "start"); s != "" {
		return "\n\n" + s + "\n" + earlier
	}
	return earlier
}

// SpecKey is the metadata key naming the spec a ticket comes from: the committed spec's path in the
// repository, which a spec run's breakdown gives every issue it files.
const SpecKey = "spec"

// specNote tells ticket t's worker that t is part of the spec its metadata names (SpecKey): to read
// the sections of it that t cites before it does what (start, carry on), and to treat it as
// settled. It is one sentence, which goes in a new worker's first message and in what a resumed
// worker is told; "" when t names no spec, or one that isn't a path on one line: the metadata is
// anyone's to change, and a resumed worker is told what it is told in one line.
func specNote(t Ticket, what string) string {
	var path string
	if json.Unmarshal(t.metadataField(SpecKey), &path) != nil {
		return ""
	}
	path = strings.TrimSpace(path)
	if path == "" || strings.ContainsFunc(path, unicode.IsControl) {
		return ""
	}
	return fmt.Sprintf("%s is part of the spec at %s: read the sections of it that %s cites before you %s, "+
		"and treat the spec as settled.", t.ID, path, t.ID, what)
}

// rulesArgs writes ticket id's worker's standing rules to its worktree wt and returns the
// arguments that append them to a Claude worker's system prompt; nil for workers of other kinds.
func (o *Loop) rulesArgs(wt, id string) ([]string, error) {
	if !o.cfg.ClaudeWorkers() {
		return nil, nil
	}
	path, err := project.WriteRules(wt, o.workerRules(id))
	if err != nil {
		return nil, fmt.Errorf("cannot write its standing rules to %s: %w", project.RunPath(project.RulesName), err)
	}
	return []string{"--append-system-prompt-file", path}, nil
}
