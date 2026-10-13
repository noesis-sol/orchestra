package project

import (
	"os"
	"strings"
	"testing"
)

// Both worker prompts, the template init writes and this repository's own, have a follow-up that
// builds on the ticket's changes blocked by it, and, under Close, a ticket that needs another's
// unmerged work reopened with a note and left to wait for that ticket to merge.
func TestWorkerPromptWaitsForUnmergedWork(t *testing.T) {
	own, err := os.ReadFile("../../.orchestra/worker-prompt.md")
	if err != nil {
		t.Fatal(err)
	}
	for name, prompt := range map[string]string{"template": promptTemplate, "repository's": string(own)} {
		closeAt := strings.Index(prompt, "\n## Close\n")
		unattended := strings.Index(prompt, "\n## Working unattended\n")
		if closeAt < 0 || unattended < closeAt {
			t.Errorf("the %s prompt should have Close, then Working unattended:\n%s", name, prompt)
			continue
		}
		flat := func(s string) string { return strings.Join(strings.Fields(s), " ") }
		rules, closing, stops := flat(prompt[:closeAt]), flat(prompt[closeAt:unattended]), flat(prompt[unattended:])
		for _, want := range []string{
			"A follow-up that changes or builds on what this ticket adds gets `--deps blocked-by:TICKET_ID`",
			"bd keeps one link per pair of tickets, so not `discovered-from` as well",
			"one independent of this ticket's changes may stay `discovered-from`",
		} {
			if !strings.Contains(rules, want) {
				t.Errorf("the %s prompt's rules lack %q:\n%s", name, want, prompt[:closeAt])
			}
		}
		for _, want := range []string{
			"If the ticket needs work from another ticket that isn't on your base yet, " +
				"don't build on that ticket's branch or copy its code, and leave nothing committed or changed",
			"`bd dep add TICKET_ID <other>`",
			"`bd update TICKET_ID --status open --append-notes " +
				`"Waiting on <other> to merge: <what you need from it>"` + "` and stop",
			"orchestra puts the ticket back in the queue until <other> merges",
		} {
			if !strings.Contains(closing, want) {
				t.Errorf("the %s prompt's Close lacks %q:\n%s", name, want, prompt[closeAt:unattended])
			}
		}
		if !strings.Contains(stops, "a wait for another ticket to merge") {
			t.Errorf("the %s prompt's stops should include the wait for a merge:\n%s", name, prompt[unattended:])
		}
	}
}
