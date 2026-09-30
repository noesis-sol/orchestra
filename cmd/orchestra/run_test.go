package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
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
	if why := stoppedBy(m.(tui.Dashboard), nil, false, nil); why != "with Ctrl+C" || !cancelled {
		t.Errorf("Ctrl+C: stopped %q, cancelled %v", why, cancelled)
	}

	ended, err := closedDashboard(t, func(_ *tea.Program, s *tui.ProgramSink) {
		s.Event(dispatch.Event{Kind: dispatch.EvDone, Text: "READY_EMPTY after 1 tickets"})
	})
	if why := stoppedBy(ended, err, false, syscall.SIGTERM); why != "" {
		t.Errorf("the loop's last event: stopped %q, want the loop's own end", why)
	}

	// A stop signal quits the dashboard the way Quit does.
	quit, err := closedDashboard(t, func(p *tea.Program, _ *tui.ProgramSink) { p.Quit() })
	for sig, want := range map[os.Signal]string{syscall.SIGTERM: "by SIGTERM", syscall.SIGHUP: "by SIGHUP", os.Interrupt: "by SIGINT"} {
		if why := stoppedBy(quit, err, false, sig); why != want {
			t.Errorf("%v while the loop runs: stopped %q, want %q", sig, why, want)
		}
	}
	if why := stoppedBy(quit, err, true, syscall.SIGHUP); why != "" {
		t.Errorf("quit after the loop ended: stopped %q", why)
	}

	if why := stoppedBy(tui.Dashboard{}, errors.New("could not open a new TTY"), false, nil); why != "because the dashboard failed" {
		t.Errorf("failed dashboard: stopped %q", why)
	}
}

// fakeOrgans writes a report and fails to save it when saveErr is set.
type fakeOrgans struct{ saveErr error }

func (fakeOrgans) FinishTriage(context.Context) {}
func (fakeOrgans) Review(context.Context, int, string) (string, error) {
	return "# Orchestra run\n\nALL MERGED\n", nil
}
func (f fakeOrgans) SaveReport(string) (string, error) {
	if f.saveErr != nil {
		return "", f.saveErr
	}
	return "/reports/r.md", nil
}

// stdoutOf returns what f prints to standard output.
func stdoutOf(t *testing.T, f func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()
	done := make(chan string)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	f()
	w.Close()
	return <-done
}

func TestReportIsShownWhenItCannotBeSaved(t *testing.T) {
	for _, tc := range []struct {
		saveErr         error
		printed, logged string
	}{
		{nil, "report saved to /reports/r.md", "REPORT written to /reports/r.md"},
		{errors.New("mkdir /reports: permission denied"), "report not saved: mkdir /reports: permission denied",
			"report not saved: mkdir /reports: permission denied"},
	} {
		log, err := dispatch.OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
		if err != nil {
			t.Fatal(err)
		}
		out := stdoutOf(t, func() {
			organPhase(fakeOrgans{tc.saveErr}, options{Review: true}, log, dispatch.ExitOK, "", tui.Printer{}, func() {})
		})
		if !strings.Contains(out, "ALL MERGED") || !strings.Contains(out, tc.printed) {
			t.Errorf("save error %v: printed\n%s", tc.saveErr, out)
		}
		if lines := strings.Join(log.RunLines(), "\n"); !strings.Contains(lines, tc.logged) {
			t.Errorf("save error %v: logged\n%s", tc.saveErr, lines)
		}
	}
}
