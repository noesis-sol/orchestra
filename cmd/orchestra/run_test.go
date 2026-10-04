package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// runIn calls run with these arguments and environment, from dir, and returns its output and result.
func runIn(t *testing.T, dir string, env map[string]string, args ...string) (string, string, error) {
	t.Helper()
	t.Chdir(dir)
	var stdout, stderr strings.Builder
	getenv := func(k string) string { return env[k] }
	err := run(context.Background(), append([]string{"orchestra"}, args...), getenv, strings.NewReader(""), &stdout, &stderr)
	return stdout.String(), stderr.String(), err
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
	_, stderr, err := runIn(t, t.TempDir(), nil)
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
	if stdout, _, err := runIn(t, dir, nil, "-version"); err != nil || !strings.HasPrefix(stdout, "orchestra ") {
		t.Errorf("-version: %v %q", err, stdout)
	}
	if _, stderr, err := runIn(t, dir, nil, "-h"); err != nil || !strings.Contains(stderr, "Usage: orchestra") || !strings.Contains(stderr, "Exit codes") {
		t.Errorf("-h: %v %q", err, stderr)
	}
	if _, _, err := runIn(t, dir, nil, "--no-such-flag"); exitOf(err) != 2 {
		t.Errorf("a bad flag should exit 2, got %d", exitOf(err))
	}
	if _, _, err := runIn(t, dir, nil, "init", "-h"); err != nil {
		t.Errorf("init -h: %v", err)
	}
}

func TestRunRefusesPositionalArguments(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-c", "2", "init"}, "init comes before its flags"},
		{[]string{"-plain", "init", "--check", "make test"}, "init comes before its flags"},
		{[]string{"-c", "2", "plan", "--apply"}, "plan comes before its flags"},
		{[]string{"foo"}, `unexpected argument "foo"`},
	} {
		_, stderr, err := runIn(t, dir, nil, tc.args...)
		if exitOf(err) != 2 || !strings.Contains(stderr, tc.want) || strings.Contains(stderr, "cannot start") {
			t.Errorf("%q: exit %d, stderr:\n%s", tc.args, exitOf(err), stderr)
		}
	}

	// 'orchestra init foo' sets nothing up.
	repo, _ := gitRepo(t)
	if _, _, err := runIn(t, repo, nil, "init", "foo"); exitOf(err) != 2 {
		t.Errorf("init foo: exit %d", exitOf(err))
	}
	if _, err := os.Stat(filepath.Join(repo, ".orchestra")); !os.IsNotExist(err) {
		t.Errorf("init foo created .orchestra/: %v", err)
	}
}

func TestMainExitStatusMapping(t *testing.T) {
	if status(dispatch.ExitOK) != nil {
		t.Error("0 is success")
	}
	if exitOf(status(dispatch.ExitStuck)) != 3 || exitOf(status(dispatch.ExitInterrupted)) != 130 {
		t.Error("codes should survive as exit statuses")
	}
}

// The exit codes are part of orchestra's interface (scripts and the skill rely on them).
func TestExitCodes(t *testing.T) {
	for name, pair := range map[string][2]int{
		"ok": {dispatch.ExitOK, 0}, "setup": {dispatch.ExitSetup, 2}, "stuck": {dispatch.ExitStuck, 3}, "tool": {dispatch.ExitTool, 4},
		"dirty": {dispatch.ExitDirty, 5}, "merge": {dispatch.ExitMerge, 6}, "interrupted": {dispatch.ExitInterrupted, 130},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s = %d, want %d", name, pair[0], pair[1])
		}
	}
}
