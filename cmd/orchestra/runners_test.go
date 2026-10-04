package main

import (
	"errors"
	"io"
	"os"
	"syscall"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// closedDashboard runs a dashboard without a terminal, applies send to it, and returns its final
// model and error.
func closedDashboard(t *testing.T, send func(*tea.Program, *tui.ProgramSink)) (tui.Dashboard, error) {
	t.Helper()
	p := tea.NewProgram(tui.NewDashboard(dispatch.Config{Limit: 40}, func() {}, func(bool) {}, func(string) {}),
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
	m, _ := tui.NewDashboard(dispatch.Config{}, func() { cancelled = true }, func(bool) {}, func(string) {}).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
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
