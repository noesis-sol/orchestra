package dispatch

import (
	"fmt"
	"strings"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A Claude worker gets its standing rules in its system prompt, and only what is particular to its
// start in its first message. Claude Code sends the system prompt with every request, so the rules
// survive when a long ticket's conversation is compacted, while a summary may lose instructions
// given early in it: never push, the ways to end (DONE, a question, a deferral), checks in the
// foreground. Workers of other kinds get the whole prompt as their first message.

// workerRules are ticket id's worker's standing rules: the worker prompt with its TICKET_ID filled
// in, and what the run adds for the whole of the ticket (its scope, its time limit).
func (o *Loop) workerRules(id string) string {
	return strings.ReplaceAll(o.prompt, "TICKET_ID", id) + o.scopeNote(id) + o.budgetNote()
}

// fullPrompt is ticket id's whole prompt as one first message, for a worker without its rules in
// its system prompt: the worker prompt, then earlier (see earlierNote) and the run's notes.
func (o *Loop) fullPrompt(id, earlier string) string {
	return strings.ReplaceAll(o.prompt, "TICKET_ID", id) + earlier + o.scopeNote(id) + o.budgetNote()
}

// ticketPrompt is the first message of ticket id's worker, its standing rules in its system prompt:
// its ticket, and earlier (see earlierNote).
func ticketPrompt(id, earlier string) string {
	return fmt.Sprintf("Your ticket is %s. Your standing rules for it are in your system prompt "+
		"(a copy is in %s): follow them exactly, from claiming the ticket to closing it.\n",
		id, project.RunPath(project.RulesName)) + earlier
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
