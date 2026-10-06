package organ

import (
	"strings"
	"testing"
)

// What the worker's hooks noted (a permission prompt, a compaction) reaches triage in a section of
// its own, left out when they noted nothing.
func TestTriageInputCarriesWhatTheHooksNoted(t *testing.T) {
	d := Deferral{ID: "k-1", How: "the worker deferred it", Ticket: "k-1 · Support visionOS",
		Hooks: "It waited on a permission prompt once, Claude Code asking to allow: Bash."}
	in := triageInput(d)
	want := "## What the worker's hooks noted (permission prompts, compactions)\n\n<evidence id=\""
	if !strings.Contains(in, want) || !strings.Contains(in, "\">\n"+d.Hooks+"\n</evidence id=\"") {
		t.Errorf("triage input lacks the hooks' section:\n%s", in)
	}
	d.Hooks = ""
	if in := triageInput(d); strings.Contains(in, "hooks noted") {
		t.Errorf("no section when the hooks noted nothing:\n%s", in)
	}
}
