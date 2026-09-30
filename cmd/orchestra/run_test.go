package main

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// runIn calls run with these arguments and environment, from dir, and returns its result and output.
func runIn(t *testing.T, dir string, env map[string]string, args ...string) (error, string, string) {
	t.Helper()
	t.Chdir(dir)
	var stdout, stderr strings.Builder
	getenv := func(k string) string { return env[k] }
	err := run(context.Background(), append([]string{"orchestra"}, args...), getenv, strings.NewReader(""), &stdout, &stderr)
	return err, stdout.String(), stderr.String()
}

func exitOf(err error) int {
	var s exitStatus
	if errors.As(err, &s) {
		return int(s)
	}
	if err != nil {
		return -1
	}
	return 0
}

func TestRunReportsSetupProblemsAndExits2(t *testing.T) {
	err, _, stderr := runIn(t, t.TempDir(), nil)
	if exitOf(err) != 2 {
		t.Errorf("exit = %d (%v)", exitOf(err), err)
	}
	for _, want := range []string{"orchestra cannot start:", "Not inside a git repository", "Not running inside a Herdr pane"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr)
		}
	}
}

func TestRunVersionHelpAndBadFlags(t *testing.T) {
	dir := t.TempDir()
	if err, stdout, _ := runIn(t, dir, nil, "-version"); err != nil || !strings.HasPrefix(stdout, "orchestra ") {
		t.Errorf("-version: %v %q", err, stdout)
	}
	if err, _, stderr := runIn(t, dir, nil, "-h"); err != nil || !strings.Contains(stderr, "Usage: orchestra") || !strings.Contains(stderr, "Exit codes") {
		t.Errorf("-h: %v %q", err, stderr)
	}
	if err, _, _ := runIn(t, dir, nil, "--no-such-flag"); exitOf(err) != 2 {
		t.Errorf("a bad flag should exit 2, got %d", exitOf(err))
	}
	if err, _, _ := runIn(t, dir, nil, "init", "-h"); err != nil {
		t.Errorf("init -h: %v", err)
	}
}

func TestMainExitStatusMapping(t *testing.T) {
	if status(exitOK) != nil {
		t.Error("0 is success")
	}
	if exitOf(status(exitStuck)) != 3 || exitOf(status(exitInterrupted)) != 130 {
		t.Error("codes should survive as exit statuses")
	}
}
