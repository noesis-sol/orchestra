package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A Claude worker's rules tell it that what the project needs of the machine is the requires list,
// naming where it is and what it has: a ticket that needs a tool the list lacks adds an entry for it,
// commits it with the ticket, and installs it with the entry's own install, never by hand; a manual
// step is a question for the maintainer. A worker of another kind has it in its whole prompt.
func TestWorkerRulesSendMachineToolsToTheRequiresList(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"claude", "codex"} {
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.cfg.AgentKind = kind
			h.cfg.Requires = []project.Requirement{
				{Name: "sh", Check: "command -v sh", Manual: "install a shell"},
				{Name: "env", Check: "command -v env", Install: "true"},
			}
			h.beads.add("A", "needs a tool", 1)
			f := &runFiles{rules: map[string]string{}, first: map[string]string{}}
			h.worker("A", f.then(finishes("a.txt")))
			if o, code := h.run(); code != ExitOK {
				t.Fatalf("%s: exit %d, final %q\n%s", kind, code, o.Final(), h.sink.text())
			}
			prompt := f.rules["A"]
			if kind != "claude" {
				if pasted := h.herdr.pastedText("A"); len(pasted) == 1 {
					prompt = pasted[0]
				}
			}
			flat := strings.Join(strings.Fields(prompt), " ")
			for _, want := range []string{
				`its requires list, "requires" in .orchestra/settings.json, which has sh, env now.`,
				"A ticket that needs one the list doesn't have adds an entry for it",
				"commits it with the ticket, and installs it with that entry's own install command, " +
					project.RequiresRule,
				"A manual step is asked of the maintainer as a question.",
			} {
				if !strings.Contains(flat, want) {
					t.Errorf("%s: the worker's prompt lacks %q:\n%s", kind, want, prompt)
				}
			}
		})
	}
}

// A worker prompt that says it already, as orchestra's template does, isn't told it again: its rules
// only say where the list is and what it has, "no entries yet" for an empty one.
func TestRequiresNoteLeavesTheRuleToAPromptThatHasIt(t *testing.T) {
	t.Parallel()
	o := &Loop{prompt: "Work on TICKET_ID: " + project.RequiresRule + "."}
	if got, want := o.requiresNote(), "\n\nThe project's requires list is \"requires\" in "+
		".orchestra/settings.json, which has no entries yet.\n"; got != want {
		t.Errorf("note %q, want %q", got, want)
	}
	o.cfg.Requires = []project.Requirement{{Name: "jq"}}
	if got := o.requiresNote(); !strings.HasSuffix(got, "which has jq now.\n") || strings.Contains(got, "adds an entry") {
		t.Errorf("note %q; want the list alone", got)
	}
	o.prompt = "Work on TICKET_ID."
	if got := o.requiresNote(); !strings.Contains(got, "adds an entry for it") {
		t.Errorf("a prompt without the rule isn't told it: %q", got)
	}
}
