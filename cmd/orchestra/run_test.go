package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
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

func TestRunRefusesPositionalArguments(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"-c", "2", "init"}, "init comes before its flags"},
		{[]string{"-plain", "init", "--check", "make test"}, "init comes before its flags"},
		{[]string{"foo"}, `unexpected argument "foo"`},
	} {
		err, _, stderr := runIn(t, dir, nil, tc.args...)
		if exitOf(err) != 2 || !strings.Contains(stderr, tc.want) || strings.Contains(stderr, "cannot start") {
			t.Errorf("%q: exit %d, stderr:\n%s", tc.args, exitOf(err), stderr)
		}
	}

	// 'orchestra init foo' sets nothing up.
	repo, _ := gitRepo(t)
	if err, _, _ := runIn(t, repo, nil, "init", "foo"); exitOf(err) != 2 {
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

// closedDashboard runs a dashboard without a terminal, applies send to it, and returns its final
// model and error.
func closedDashboard(t *testing.T, send func(*tea.Program, *tui.ProgramSink)) (tui.Dashboard, error) {
	t.Helper()
	p := tea.NewProgram(tui.NewDashboard(dispatch.Config{Limit: 40}, func() {}),
		tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutSignalHandler())
	type result struct {
		m   tea.Model
		err error
	}
	done := make(chan result, 1)
	go func() {
		m, err := p.Run()
		done <- result{m, err}
	}()
	send(p, tui.NewProgramSink(p))
	r := <-done
	m, _ := r.m.(tui.Dashboard)
	return m, r.err
}

func TestAnyEarlyEndOfTheDashboardStopsTheLoop(t *testing.T) {
	cancelled := false
	m, _ := tui.NewDashboard(dispatch.Config{}, func() { cancelled = true }).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if why := stoppedBy(m.(tui.Dashboard), nil, false); why != "with Ctrl+C" || !cancelled {
		t.Errorf("Ctrl+C: stopped %q, cancelled %v", why, cancelled)
	}

	ended, err := closedDashboard(t, func(_ *tea.Program, s *tui.ProgramSink) {
		s.Event(dispatch.Event{Kind: dispatch.EvDone, Text: "READY_EMPTY after 1 tickets"})
	})
	if why := stoppedBy(ended, err, false); why != "" {
		t.Errorf("the loop's last event: stopped %q, want the loop's own end", why)
	}

	// Bubble Tea turns SIGTERM into the QuitMsg that Quit sends.
	quit, err := closedDashboard(t, func(p *tea.Program, _ *tui.ProgramSink) { p.Quit() })
	if why := stoppedBy(quit, err, false); why != "by SIGTERM" {
		t.Errorf("quit while the loop runs: stopped %q", why)
	}
	if why := stoppedBy(quit, err, true); why != "" {
		t.Errorf("quit after the loop ended: stopped %q", why)
	}

	if why := stoppedBy(tui.Dashboard{}, tea.ErrInterrupted, false); why != "by SIGINT" {
		t.Errorf("SIGINT: stopped %q", why)
	}
	if why := stoppedBy(tui.Dashboard{}, errors.New("could not open a new TTY"), false); why != "because the dashboard failed" {
		t.Errorf("failed dashboard: stopped %q", why)
	}
}

func TestInterruptLineNamesTheWorkersLeftRunning(t *testing.T) {
	if got := interruptLine("with Ctrl+C", nil); got != "INTERRUPTED: stopped with Ctrl+C; a running worker keeps its tab and worktree" {
		t.Errorf("no workers: %q", got)
	}
	got := interruptLine("by SIGTERM", []dispatch.Status{{Ticket: "a-1", Tab: "w1:2"}, {Ticket: "a-2", Tab: "w1:3"}})
	if got != "INTERRUPTED: stopped by SIGTERM while a-1 (tab w1:2), a-2 (tab w1:3) were running; their tabs and worktrees are left open" {
		t.Errorf("two workers: %q", got)
	}
}
