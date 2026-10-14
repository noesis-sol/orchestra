package project

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
)

// Workers can run in cmux, so a machine with cmux alone has a terminal for them, and init's last
// step says to start the run from a cmux terminal.
func TestStartRunFromACmuxTerminal(t *testing.T) {
	for _, tc := range []struct {
		installed []string
		want      string
	}{
		{[]string{"cmux"}, "From a cmux terminal, on the branch"},
		{[]string{"herdr", "cmux"}, "From a Herdr pane, or a cmux terminal, on the branch"},
		{[]string{"cmux", "tmux"}, "From a cmux terminal, or any terminal with tmux, on the branch"},
		{[]string{"herdr", "cmux", "tmux"}, "From a Herdr pane, a cmux terminal, or any terminal with tmux, on the branch"},
		{nil, "From a Herdr pane, a cmux terminal, or any terminal with tmux, on the branch"},
	} {
		got := startRun(func(cmd string) bool { return slices.Contains(tc.installed, cmd) })
		if !strings.HasPrefix(got, tc.want) || !strings.HasSuffix(got, ":\norchestra") {
			t.Errorf("with %v: %q, want it to start %q", tc.installed, got, tc.want)
		}
	}
}

// onPath is Prerequisites' and NextSteps' has for the tests that don't choose: the commands on the PATH.
func onPath(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

// A machine with cmux alone, wherever it is (outside a cmux terminal its CLI is in the app, which
// the has given finds), has a terminal for the workers.
func TestPrerequisitesTakeCmuxAlone(t *testing.T) {
	terminal := func(installed ...string) Step {
		for _, s := range Prerequisites(t.TempDir(), os.Getenv, func(cmd string) bool {
			return slices.Contains(installed, cmd)
		}) {
			if s.Label == "herdr, tmux or cmux" {
				return s
			}
		}
		t.Fatal("no step for the workers' terminal")
		return Step{}
	}
	if s := terminal(); s.Kind != StepMissing || !strings.Contains(s.Detail, "cmux") {
		t.Errorf("without cmux: %+v, want it missing, with cmux among the fixes", s)
	}
	if s := terminal("cmux"); s.Kind != StepDone {
		t.Errorf("with cmux: %+v, want it there", s)
	}
}
