package project

import (
	"os"
	"path/filepath"
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

// Outside a cmux terminal its CLI is not on the PATH but in the app, which counts as cmux being there.
func TestPrerequisitesFindCmuxInItsApp(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no herdr, tmux or cmux on it
	app := filepath.Join(t.TempDir(), "cmux")
	old := cmuxApp
	cmuxApp = app
	t.Cleanup(func() { cmuxApp = old })

	terminal := func() Step {
		for _, s := range Prerequisites(t.TempDir(), os.Getenv) {
			if s.Label == "herdr, tmux or cmux" {
				return s
			}
		}
		t.Fatal("no step for the workers' terminal")
		return Step{}
	}
	if s := terminal(); s.Kind != StepMissing || !strings.Contains(s.Detail, "cmux") {
		t.Errorf("without the cmux app: %+v, want it missing, with cmux among the fixes", s)
	}
	if err := os.WriteFile(app, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if s := terminal(); s.Kind != StepDone {
		t.Errorf("with the cmux app: %+v, want it there", s)
	}
	if !onHost("cmux") || onHost("tmux") {
		t.Error("onHost finds cmux only in its app, or tmux with none")
	}
}
