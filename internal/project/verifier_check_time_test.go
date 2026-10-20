package project

import (
	"strings"
	"testing"
)

// The verifier, which has Bash but not Monitor, gives a check Bash's longest timeout, and waits on one
// that can take longer with short waits in the foreground: verifiers ran checks with Bash's default 2
// minutes, which moved them to the background, then were refused a long sleep to wait on them.
func TestTheVerifierGivesACheckItsTime(t *testing.T) {
	agent := VerifierText(nil, []string{"scripts/check.sh"}, nil, nil, nil)
	for _, want := range []string{
		"- Give a check its time: Bash waits 2 minutes on a command unless told otherwise, then moves it to\n" +
			"  the background. Pass Bash `timeout: 600000`, its maximum (10 minutes), for every check.",
		"as the merge check may (`check_fast_timeout` in `.orchestra/settings.json`,\n  " +
			DefaultCheckTimeoutText + " when unset)",
		"(`<command> > <file> 2>&1; echo \"exit $?\" >> <file>`)",
		"`until grep -q '^exit ' <file>; do sleep 10; done`",
		"Never `sleep` for long:\n  Bash refuses it.\n",
	} {
		if !strings.Contains(agent, want) {
			t.Errorf("the verifier lacks %q:\n%s", want, agent)
		}
	}
	if !strings.HasPrefix(agent, "---\n") || !strings.Contains(agent, "\ntools: Read, Grep, Glob, Bash\n") {
		t.Errorf("the verifier's tools changed:\n%s", agent)
	}
}
