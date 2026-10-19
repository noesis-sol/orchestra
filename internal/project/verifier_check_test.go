package project

import (
	"strings"
	"testing"
)

// The worker gives the verifier the commit at which its check passed, and the verifier runs the check
// again only where that commit isn't HEAD, the worktree has changes, or the slower suites weren't run:
// in most rounds the worker had just passed the check and changed nothing since.
func TestTheVerifierUsesTheWorkersPassingCheck(t *testing.T) {
	agent := VerifierText(nil, []string{"scripts/check.sh"}, []string{"npm run e2e"})
	for _, want := range []string{
		"Give it the ticket's ID and the commit at which the checks passed.\n",
		"3. The worker tells you the commit at which `" + FastRunner + "` passed, whether it ran `" +
			FullRunner + "`\n   too, and the file their output is in where it kept one.",
		// The commit is HEAD and the worktree clean: no check.
		"When `git rev-parse HEAD` is that commit\n   and `git status --porcelain` prints nothing, that " +
			"result stands: quote it (the end of the file,\n   where it gave one) rather than run the check again.",
		// Another commit, changes, or no commit given: the check.
		"Otherwise, or when it gave no commit, run\n   `" + FastRunner + "` in the foreground, and quote " +
			"the command and the end of its output.",
		"When the\n   change touches what the slower suites cover and the worker didn't run `" + FullRunner +
			"` at HEAD,\n   run `" + FullRunner + "` too.\n",
	} {
		if !strings.Contains(agent, want) {
			t.Errorf("the verifier lacks %q:\n%s", want, agent)
		}
	}
	for _, want := range []string{
		"give it the ticket's ID, TICKET_ID,\n  the commit at which the check passed (`git rev-parse HEAD`), " +
			"whether you ran `" + FullRunner + "`\n  there too, and the file their output is in where you kept " +
			"one, so that it needn't run them again;\n  nothing of your reasoning.",
		"and ask it again with the new commit.",
	} {
		if !strings.Contains(verifierSection, want) {
			t.Errorf("the Verify section lacks %q:\n%s", want, verifierSection)
		}
	}
}
