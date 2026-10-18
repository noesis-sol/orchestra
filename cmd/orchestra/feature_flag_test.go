//go:build darwin || linux

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// --feature makes its own scope, so --ticket with it is a setup problem, as is an empty request.
// --refine goes with it, and changes nothing: a feature is always talked through.
func TestConfigFeatureFlags(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("skipped by -short: loads the configuration on real git per case")
	}
	configFixture(t, `{"concurrent": 1}`)
	if c, p := loadWith(t, "--feature", "  Add a --json flag \n"); len(p) > 0 || c.Feature != "Add a --json flag" {
		t.Errorf("feature: %q, problems %v", c.Feature, p)
	}
	if c, p := loadWith(t, "--feature", "Add a flag", "--refine"); len(p) > 0 || c.Feature != "Add a flag" {
		t.Errorf("feature with --refine: %q, problems %v", c.Feature, p)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--feature", ""}, "--feature needs the request"},
		{[]string{"--feature", " \t\n"}, "--feature needs the request"},
		{[]string{"--feature", "Add a flag", "--ticket", "k-1"}, "--feature can't be combined with --ticket"},
		{[]string{"--refine"}, "--refine only applies with --spec or --feature."},
	} {
		if _, p := loadWith(t, tc.args...); !strings.Contains(strings.Join(p, "\n"), tc.want) {
			t.Errorf("%q: problems %q, want %q", tc.args, p, tc.want)
		}
	}
	setenv(t, "ORCHESTRA_TICKET", "k-1")
	if _, p := loadWith(t, "--feature", "Add a flag"); !strings.Contains(strings.Join(p, "\n"), "can't be combined") {
		t.Errorf("ORCHESTRA_TICKET with --feature: problems %q", p)
	}
}

// A feature run with no ticket left under a limit given would file its plan and start none of it,
// so it doesn't start; a run without --feature still ends at once with LIMIT_REACHED.
func TestFeatureNeedsRoomUnderTheLimit(t *testing.T) {
	t.Parallel()
	configFixture(t, `{"concurrent": 1}`)
	const want = "--feature would file a plan and run none of it"
	for _, tc := range []struct {
		env  map[string]string
		args []string
	}{
		{map[string]string{"DONE_SO_FAR": "40", "LIMIT": "40"}, nil},
		{map[string]string{"LIMIT": "0"}, nil},
		{nil, []string{"--limit", "3", "--done-so-far", "5"}},
	} {
		for k, v := range tc.env {
			setenv(t, k, v)
		}
		_, p := loadWith(t, append([]string{"--feature", "Add a flag"}, tc.args...)...)
		if len(p) != 1 || !strings.Contains(p[0], want) {
			t.Errorf("%v %q: problems %q", tc.env, tc.args, p)
		}
		if _, p := loadWith(t, tc.args...); len(p) > 0 {
			t.Errorf("%v %q without --feature: problems %q", tc.env, tc.args, p)
		}
		for k := range tc.env {
			setenv(t, k, "")
		}
	}
	if _, p := loadWith(t, "--feature", "Add a flag", "--limit", "3", "--done-so-far", "2"); len(p) > 0 {
		t.Errorf("one ticket left: problems %q", p)
	}
}

// --yes is gone: given, with --feature or without, even as --yes=false, it ends the run, exit 2,
// naming --spec as the way to run a request unattended, before claude runs or anything is filed.
// The help leaves it out.
func TestYesIsGone(t *testing.T) {
	t.Parallel()
	const want = "--yes is gone: --feature always talks the feature through with you; " +
		"orchestra --spec <file> runs a request written to a file unattended."
	configFixture(t, `{"concurrent": 1}`)
	for _, args := range [][]string{{"--feature", "Add a flag", "--yes"}, {"--yes"}, {"--yes=false"}} {
		if _, p := loadWith(t, args...); len(p) != 1 || p[0] != want {
			t.Errorf("%q: problems %q", args, p)
		}
	}
	var help strings.Builder
	if _, _, err := loadConfig(testCtx(context.Background(), t), workDir(t), []string{"-h"}, getenv(t), &help); err == nil ||
		strings.Contains(help.String(), "--yes") || !strings.Contains(help.String(), "--feature string") {
		t.Errorf("the help (%v):\n%s", err, help.String())
	}

	dir := featureTools(t)
	_, stdout, stderr, code := runFeatureIn(t, "--feature", "Add a --json flag", "--yes")
	if code != dispatch.ExitSetup || stderr != "orchestra cannot start:\n  - "+want+"\n" || stdout != "" {
		t.Errorf("exit %d, stdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "claude-n")); !os.IsNotExist(err) {
		t.Errorf("claude ran: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "bd-calls")); !os.IsNotExist(err) {
		t.Errorf("bd was asked: %v", err)
	}
}

// --feature hands the request to the spec breakdown's session in Feature mode, as a feature typed at
// the start does, with --refine or without: on orchestra's terminal here, with no question asked
// first and nothing asked of the organs. Once it hands the epic back, orchestra says what was filed
// and runs it without asking; the lock and the run's start name the feature.
func TestFeatureFlagIsTalkedThrough(t *testing.T) {
	// Not parallel: its claude (interviewTools) sends the test's process SIGINT, which the runs of tests
	// beside it would catch too.
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"alone", nil},
		{"with --refine", []string{"--refine"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := interviewTools(t, "f-1")
			term, repo, exit := runOnTerminal(t, append([]string{"--feature", paneRequest}, tc.args...)...)
			code, stderr := exit()
			if code != dispatch.ExitOK || stderr != "" {
				t.Fatalf("exit %d, stderr:\n%s\nthe terminal:\n%s", code, stderr, screenOf(term))
			}
			prompt := filepath.Join(realPath(repo), project.RunPath("spec-prompt.md"))
			args := read(t, filepath.Join(dir, "claude-args"))
			if !strings.HasPrefix(args, "[--append-system-prompt-file]\n["+prompt+"]\n[--effort]\n[high]\n[--]\n[") {
				t.Errorf("claude ran with:\n%s", args)
			}
			if text := terminalMessage(t, repo, args); text != paneRequest {
				t.Errorf("claude's first message holds the description %q", text)
			}
			if instructions := read(t, filepath.Join(dir, "claude-prompt")); !strings.Contains(instructions,
				"# Feature breakdown") {
				t.Errorf("the instructions aren't the Feature mode's:\n%s", instructions)
			}

			out := screenOf(term)
			for _, want := range []string{"Talking the feature through with claude", "fake claude: bye",
				"filed the feature as f-1, JSON output (2 tickets)\n", "○ Nothing under f-1 is ready to run"} {
				if !strings.Contains(out, want) {
					t.Errorf("the terminal lacks %q:\n%s", want, out)
				}
			}
			for _, not := range []string{"Current tickets", "creening", "lanning", "Start the run", "[y/N]"} {
				if strings.Contains(out, not) {
					t.Errorf("the terminal shows %q:\n%s", not, out)
				}
			}
			if calls := read(t, filepath.Join(dir, "bd-calls")); strings.Contains(calls, "create|") {
				t.Errorf("orchestra filed tickets itself:\n%s", calls)
			}
			events := read(t, filepath.Join(repo, project.RunPath(dispatch.EventsName)))
			if !strings.Contains(events, `"scope":"f-1"`) ||
				!strings.Contains(events, `"feature":"- Add a --json flag\n- to the list command"`) {
				t.Errorf("the run's start doesn't name the epic and the feature:\n%s", events)
			}
			var h project.Holder
			if err := json.Unmarshal([]byte(read(t, filepath.Join(repo, ".git", project.LockName))), &h); err != nil {
				t.Fatal(err)
			}
			if h.Feature != paneRequest {
				t.Errorf("the lock names the feature %q", h.Feature)
			}
			if log := read(t, filepath.Join(repo, ".orchestra", "orchestra.log")); !strings.Contains(log,
				"FEATURE interview: - Add a --json flag") {
				t.Errorf("the log doesn't name the feature:\n%s", log)
			}
		})
	}
}

// Without a terminal to talk it through on, and no pane, --feature ends the run, exit 2, before
// claude runs or anything is filed, naming --spec as the way to run a request unattended.
func TestFeatureFlagWithoutATerminal(t *testing.T) {
	t.Parallel()
	dir := featureTools(t)
	setenv(t, "HERDR_PANE_ID", "")
	_, stdout, stderr, code := runFeatureIn(t, "--feature", "Add a --json flag", "--plain")
	if want := "orchestra couldn't talk the feature through: talking the feature through needs you at a " +
		"terminal, and orchestra's input or output isn't one: orchestra --spec <file> breaks a request written " +
		"to a file down unattended\n"; code != dispatch.ExitSetup || !strings.HasSuffix(stderr, want) {
		t.Errorf("exit %d, stderr:\n%s\nwant it to end in:\n%s", code, stderr, want)
	}
	if strings.Contains(stdout, "filed") {
		t.Errorf("stdout:\n%s", stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "claude-n")); !os.IsNotExist(err) {
		t.Errorf("claude ran: %v", err)
	}
	if calls, err := os.ReadFile(filepath.Join(dir, "bd-calls")); err == nil && strings.Contains(string(calls), "f-1") {
		t.Errorf("bd was asked about an epic:\n%s", calls)
	}
}

// The run's startup checks come first: a --feature run that couldn't start talks nothing through.
func TestFeatureStartupProblemsComeFirst(t *testing.T) {
	t.Parallel()
	dir := featureTools(t)
	repo := configFixture(t, `{"concurrent": 1}`)
	if err := os.WriteFile(filepath.Join(repo, "dirty.go"), []byte("package x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut strings.Builder
	err := run(testCtx(context.Background(), t), []string{"orchestra", "--feature", "Add a flag"}, testCmdEnv(t, strings.NewReader(""), &out, &errOut))
	if exitOf(err) != dispatch.ExitDirty || !strings.Contains(errOut.String(), "uncommitted changes in") {
		t.Errorf("dirty checkout: exit %d, stderr:\n%s", exitOf(err), errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "claude-n")); !os.IsNotExist(err) {
		t.Errorf("claude ran with a dirty checkout: %v", err)
	}

	setenv(t, "HERDR_ENV", "")
	setenv(t, "ORCHESTRA_TERMINAL", "herdr")
	errOut.Reset()
	err = run(testCtx(context.Background(), t), []string{"orchestra", "--feature", "Add a flag"}, testCmdEnv(t, strings.NewReader(""), &out, &errOut))
	if exitOf(err) != dispatch.ExitSetup || !strings.Contains(errOut.String(), "Not running inside a Herdr pane") {
		t.Errorf("outside Herdr: exit %d, stderr:\n%s", exitOf(err), errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "claude-n")); !os.IsNotExist(err) {
		t.Errorf("claude ran outside Herdr: %v", err)
	}
}
