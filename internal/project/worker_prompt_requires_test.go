package project

import (
	"strings"
	"testing"
)

// The worker prompt's template sends what a ticket needs of the machine to the requires list, in its
// opening: an entry committed with the ticket and installed with its own install, never by hand, and a
// manual step asked of the maintainer.
func TestWorkerPromptSendsMachineToolsToTheRequiresList(t *testing.T) {
	opening := sections(promptTemplate)[0].text
	flat := strings.Join(strings.Fields(opening), " ")
	for _, want := range []string{
		"What the project needs of the machine (a tool, a service, a permission) is its requires list, " +
			"`\"requires\"` in `.orchestra/settings.json`.",
		"A ticket that needs one the list doesn't have adds an entry for it",
		"an install command that runs without sudo, or a manual step for a person",
		"commits it with the ticket, and installs it with that entry's own install command, " + RequiresRule,
		"A manual step is asked of the maintainer as a question",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the template's opening lacks %q:\n%s", want, opening)
		}
	}
}
