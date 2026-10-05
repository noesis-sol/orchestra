package project

import (
	"strings"
	"testing"
)

func TestRunnerCommandsAreTheSuitesLines(t *testing.T) {
	script := FullScript([]Suite{{Name: "e2e", Command: "npm run e2e", Serial: true}, {Command: "make lint"}}, false)
	if got := strings.Join(RunnerCommands(script), "\n"); got != FastRunner+"\nnpm run e2e\nmake lint" {
		t.Errorf("commands of init's runner:\n%s", got)
	}
	if got := RunnerCommands(FastScript(nil)); strings.Join(got, "\n") != "exit 0" {
		t.Errorf("commands of a runner that checks nothing: %q", got)
	}
	// A runner the project edited shows what it runs, a marker line that does more than print the
	// marker included.
	edited := "#!/bin/sh\nset -e\n# the project's own\nprintf '%s\\n' 'SUITE: it'\\''s'\nmake test || exit 1\n" +
		"lock db && make db\nprintf '%s\\n' 'SUITE: x'; rm -f y; echo 'z'\n"
	if got := strings.Join(RunnerCommands(edited), "\n"); got != "make test || exit 1\nlock db && make db\n"+
		"printf '%s\\n' 'SUITE: x'; rm -f y; echo 'z'" {
		t.Errorf("commands of an edited runner:\n%s", got)
	}
}
