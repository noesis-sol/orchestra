package project

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRunsRunner(t *testing.T) {
	for command, want := range map[string]bool{
		FastRunner: true, FullRunner: true, "./" + FastRunner: true, "sh " + FastRunner: true,
		"bash " + FastRunner: true, "$PWD/" + FastRunner: true, "${PWD}/" + FullRunner: true,
		"$(pwd)/" + FastRunner: true, "/home/me/repo/" + FastRunner: true, "sh -c '" + FullRunner + "'": true,
		"make lint && " + FullRunner + " -v": true, "make lint;" + FastRunner: true,
		"scripts/check.sh": false, "npm test": false, FastRunner + ".bak": false, "scripts/check-fast.shx": false,
		"": false,
	} {
		if got := RunsRunner(command); got != want {
			t.Errorf("RunsRunner(%q) = %v, want %v", command, got, want)
		}
	}
}

// A check command that is just the runner, however it is run, keeps the runner as it is.
func TestRunnerSuitesKnowsTheRunnerHoweverItIsRun(t *testing.T) {
	for _, check := range []string{FastRunner, " ./" + FastRunner, "sh " + FastRunner, "bash " + FastRunner,
		"$PWD/" + FastRunner, "/home/me/repo/" + FastRunner} {
		if got := RunnerSuites(check, "--check-fast", FastRunner); got != nil {
			t.Errorf("RunnerSuites(%q) = %+v, want nil: the runner as it is", check, *got)
		}
	}
	if got := RunnerSuites("bash "+FastRunner+" -v", "--check-fast", FastRunner); got == nil {
		t.Error("a runner run with arguments is a suite, which init then leaves out")
	}
}

// No runner init writes runs a runner, but check-full.sh's first line, check-fast.sh: the suites that
// do are left out, each with a note.
func TestApplyRunnersLeavesOutSuitesThatRunARunner(t *testing.T) {
	repo := t.TempDir()
	c := Choice{
		Fast: &[]Suite{
			{Name: "check-fast", Command: "bash " + FastRunner, FoundIn: "scripts/check-fast.sh:1"}, // the scout's
			{Name: "unit tests", Command: "npm test", FoundIn: "package.json:7"},
			{Command: "$PWD/" + FullRunner, FoundIn: "--check-fast"},
		},
		Full: &[]Suite{
			{Command: "/home/me/repo/" + FullRunner, FoundIn: "--check-full"},
			{Command: "./" + FastRunner},
			{Name: "e2e", Command: "npm run e2e"},
		},
	}
	steps, err := ApplyRunners(repo, c)
	if err != nil {
		t.Fatal(err)
	}
	fast := RunnerCommands(read(t, filepath.Join(repo, FastRunner)))
	if !slices.Equal(fast, []string{"npm test"}) {
		t.Errorf("check-fast.sh runs %q, want only npm test", fast)
	}
	full := RunnerCommands(read(t, filepath.Join(repo, FullRunner)))
	if !slices.Equal(full, []string{FastRunner, "npm run e2e"}) {
		t.Errorf("check-full.sh runs %q, want check-fast.sh, then npm run e2e", full)
	}
	var notes []string
	for _, s := range steps {
		if s.Kind == StepCaution {
			notes = append(notes, s.Label+": "+s.Detail)
		}
	}
	want := []string{
		"check-fast: left out 'bash " + FastRunner + "' (from scripts/check-fast.sh:1): it runs a runner, and " +
			FastRunner + " would run itself until fork fails",
		"check-fast: left out '$PWD/" + FullRunner + "' (from --check-fast): it runs a runner, and " +
			FastRunner + " would run itself until fork fails",
		"check-full: left out '/home/me/repo/" + FullRunner + "' (from --check-full): it runs a runner, and " +
			FullRunner + " would run itself until fork fails",
		"check-full: left out './" + FastRunner + "': it runs " + FastRunner + ", which " + FullRunner +
			" runs first already",
	}
	if !slices.Equal(notes, want) {
		t.Errorf("notes:\n%s\nwant:\n%s", strings.Join(notes, "\n"), strings.Join(want, "\n"))
	}

	// Again: the runners are what init would write, kept without a note.
	runners, _ := PlanRunners(repo, c)
	if runners[0].Differs || runners[1].Differs || len(runners[0].Left) != 2 || len(runners[1].Left) != 2 {
		t.Errorf("plan again: %+v", runners)
	}
	steps, _ = ApplyRunners(repo, c)
	if len(steps) != 2 || steps[0].Kind != StepKept || steps[1].Kind != StepKept {
		t.Errorf("again: %+v", steps)
	}
}

// A check-fast whose only suite runs a runner checks nothing, and so does check-full without suites.
func TestARunnerOfOnlyRunnersChecksNothing(t *testing.T) {
	repo := t.TempDir()
	c := Choice{Fast: &[]Suite{{Command: FullRunner, FoundIn: "--check-fast"}}, Full: &[]Suite{}, Concurrent: 1}
	if _, err := ApplyRunners(repo, c); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{FastRunner, FullRunner} {
		if got := RunnerCommands(read(t, filepath.Join(repo, path))); !slices.Equal(got, []string{"exit 0"}) {
			t.Errorf("%s runs %q, want just exit 0", path, got)
		}
	}
	if err := os.MkdirAll(filepath.Join(repo, Dir), 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := ApplySettings(repo, c)
	if err != nil || !strings.Contains(s.Detail, "check-fast checks nothing yet") || strings.Contains(s.Detail, FullRunner) {
		t.Errorf("settings: %+v, %v", s, err)
	}
}
