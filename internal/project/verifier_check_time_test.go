package project

import (
	"strings"
	"testing"
)

// The verifier, which has Bash but not Monitor, passes Bash no timeout where its default is as long as a
// check may take, as a worker's settings raise it: a timeout of its own, Bash's 10-minute maximum,
// shortened the raised default and moved a longer check to the background. Elsewhere it gives a check
// Bash's longest timeout, and waits on one that can take longer with short waits in the foreground:
// verifiers ran checks with Bash's default 2 minutes, which moved them to the background, then were
// refused a long sleep to wait on them.
func TestTheVerifierGivesACheckItsTime(t *testing.T) {
	agent := VerifierText(nil, []string{"scripts/check.sh"}, nil, nil, nil)
	for _, want := range []string{
		"- Give a check its time: Bash waits on a command for the default timeout its description gives (2\n" +
			"  minutes unless raised), then moves it to the background.",
		"Where that default is as long as a check\n  may take, as in an orchestra worker, whose settings raise " +
			"it to the merge check's limit\n  (`check_fast_timeout` in `.orchestra/settings.json`, " +
			DefaultCheckTimeoutText + " when unset), pass Bash no\n  timeout: one of your own shortens it.",
		"Elsewhere, pass Bash `timeout: 600000`, its maximum (10\n  minutes), for every check,",
		"(`<command> > <file> 2>&1; echo \"exit $?\" >> <file>`)",
		"`until grep -q '^exit ' <file>; do sleep 10; done`",
		"Never `sleep` for long: Bash refuses it.\n",
	} {
		if !strings.Contains(agent, want) {
			t.Errorf("the verifier lacks %q:\n%s", want, agent)
		}
	}
	if !strings.HasPrefix(agent, "---\n") || !strings.Contains(agent, "\ntools: Read, Grep, Glob, Bash\n") {
		t.Errorf("the verifier's tools changed:\n%s", agent)
	}
}
