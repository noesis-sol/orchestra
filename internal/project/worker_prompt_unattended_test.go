package project

import (
	"os"
	"strings"
	"testing"
)

// Both worker prompts, the template init writes and this repository's own, name the four early
// stops, tell the worker to drop an invitation to redirect it and that its context is compacted,
// and end with Working unattended, after Close, then the DONE line.
func TestWorkerPromptEndsWithWorkingUnattended(t *testing.T) {
	own, err := os.ReadFile("../../.orchestra/worker-prompt.md")
	if err != nil {
		t.Fatal(err)
	}
	for name, prompt := range map[string]string{"template": promptTemplate, "repository's": string(own)} {
		closeAt := strings.Index(prompt, "\n## Close\n")
		unattended := strings.Index(prompt, "\n## Working unattended\n")
		if closeAt < 0 || unattended < closeAt {
			t.Errorf("the %s prompt should have Working unattended after Close:\n%s", name, prompt)
			continue
		}
		section := prompt[unattended:]
		if !strings.HasSuffix(section, "\n\nWhen you are finished, say DONE and stop.\n") ||
			strings.Contains(section[1:], "\n## ") {
			t.Errorf("the %s prompt should end with Working unattended, then the DONE line:\n%s", name, section)
		}
		flat := strings.Join(strings.Fields(section), " ")
		for _, want := range []string{
			"a summary that announces your next step",
			"an offer to go on",
			"a choice that doesn't block the rest",
			"a report because the turn has been long or a part is done",
			"inviting the maintainer to redirect you or offering to wait, delete it and do the next thing",
			"Your context is compacted as it fills, so don't cut work short because the conversation is long",
			"keep the checklist and the ticket's notes current",
		} {
			if !strings.Contains(flat, want) {
				t.Errorf("the %s prompt's Working unattended lacks %q:\n%s", name, want, section)
			}
		}
	}
}
