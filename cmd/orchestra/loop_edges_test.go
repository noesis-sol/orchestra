package main

import (
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// orchestra-xj6z.10: the edges of signals and exits around the run loop.

// A drain signal that comes before the loop listens waits for it, and drains it as soon as it
// begins; while the loop listens, each one goes to it at once; once it has ended, they wait again.
func TestADrainSignalBeforeTheLoopDrainsItAsItBegins(t *testing.T) {
	t.Parallel()
	w := &drainWatch{}
	w.deliver() // during the pre-flight, say
	w.deliver()
	drains := 0
	w.to(func() { drains++ })
	if drains != 1 {
		t.Errorf("the loop got %d drains as it began; want the one waiting", drains)
	}
	w.deliver()
	if drains != 2 {
		t.Errorf("the loop got %d drains in all; want the one while it listened too", drains)
	}
	w.to(nil)
	w.deliver()
	if drains != 2 {
		t.Errorf("a drain after the loop ended went to it: %d", drains)
	}
	w.to(func() { drains++ })
	if drains != 3 {
		t.Errorf("the next loop got %d drains; want the one waiting", drains-2)
	}
}

// A failed dashboard ends the run as a terminal failure, exit code 4, saying why: 130 is a stop
// asked for, with Ctrl+C, SIGTERM or SIGHUP.
func TestAFailedDashboardIsATerminalFailure(t *testing.T) {
	t.Parallel()
	stop := stoppedBy(tui.Dashboard{}, errors.New("could not open a new TTY: device not configured\nmore"), false, nil)
	if stop.code != dispatch.ExitTool {
		t.Errorf("exit %d, want %d", stop.code, dispatch.ExitTool)
	}
	ev := stop.event([]dispatch.Status{{Ticket: "a-1", Tab: "w1:2"}})
	want := "TERMINAL_FAILED: stopped because the dashboard failed (could not open a new TTY: device not configured) " +
		"while a-1 (tab w1:2) were running; their tabs and worktrees are left open"
	if ev.Kind != dispatch.EvStop || ev.Detail != dispatch.TerminalFailed || ev.Text != want {
		t.Errorf("event %v %q:\n%s\nwant %s %q:\n%s", ev.Kind, ev.Detail, ev.Text, dispatch.EvStop,
			dispatch.TerminalFailed, want)
	}
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP} {
		if stop := stoppedBy(tui.Dashboard{}, errors.New("closed"), false, sig); stop.code != dispatch.ExitInterrupted ||
			stop.event(nil).Detail != dispatch.Interrupted {
			t.Errorf("%v as the dashboard failed: exit %d, %q", sig, stop.code, stop.event(nil).Detail)
		}
	}
}

// dashboardAfterItsEnd runs a dashboard without a terminal that gets the loop's last event and then
// Ctrl+C, queued behind it before the dashboard quits, and returns its final model.
func dashboardAfterItsEnd(t *testing.T) tui.Dashboard {
	t.Helper()
	cancelled := false
	quits := 0
	// The first quit, the last event's, waits for the key, which a person could press as it comes.
	keepFirstQuit := func(_ tea.Model, msg tea.Msg) tea.Msg {
		if _, ok := msg.(tea.QuitMsg); ok {
			if quits++; quits == 1 {
				return nil
			}
		}
		return msg
	}
	p := tea.NewProgram(tui.NewDashboard(dispatch.Config{Limit: 40}, func() { cancelled = true }, func(bool) {}, func(string) {}),
		tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutSignalHandler(), tea.WithFilter(keepFirstQuit))
	done := make(chan tea.Model, 1)
	go func() {
		m, _ := p.Run()
		done <- m
	}()
	tui.NewProgramSink(p).Event(dispatch.Event{Kind: dispatch.EvDone, Text: "READY_EMPTY after 1 tickets"})
	p.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	select {
	case m := <-done:
		d, _ := m.(tui.Dashboard)
		if d.Final() == nil || !d.Interrupted() || !cancelled {
			t.Fatalf("the dashboard should have both the loop's end and Ctrl+C: final %v, interrupted %v",
				d.Final(), d.Interrupted())
		}
		return d
	case <-time.After(5 * time.Second):
		p.Kill()
		t.Fatal("the dashboard didn't quit")
	}
	return tui.Dashboard{}
}

// Ctrl+C pressed as the loop ends, queued behind its last event, doesn't add a stop after the
// loop's end nor take its code: the loop's end decides. It skips the organ phase instead.
func TestCtrlCAfterTheLoopsEndKeepsItsCode(t *testing.T) {
	t.Parallel()
	m := dashboardAfterItsEnd(t)
	if !loopEnded(m, false, 0) {
		t.Error("the dashboard showed the loop's end, but it didn't count as ended")
	}
	if stop := stoppedBy(m, nil, loopEnded(m, true, dispatch.ExitOK), nil); stop.why != "" {
		t.Errorf("stopped %q after the loop's end; want the loop's own end", stop.why)
	}
	// The loop ended with its code before the dashboard heard its end, and before the key.
	if !loopEnded(tui.Dashboard{}, true, dispatch.ExitStuck) {
		t.Error("the loop's own code, returned, should count as its end")
	}
	// Ctrl+C in the dashboard stopped the loop, which returned with the stop's code: that's no end of its own.
	ctrlC, _ := tui.NewDashboard(dispatch.Config{}, func() {}, func(bool) {}, func(string) {}).Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	if loopEnded(ctrlC.(tui.Dashboard), true, dispatch.ExitInterrupted) {
		t.Error("the loop stopped by Ctrl+C counted as ending by itself")
	}

	stops := catchStops()
	defer stops.release()
	stops.on(nil) // the dashboard has closed
	stops.carry(os.Interrupt)
	if out := organRun(t, stops); out != "" {
		t.Errorf("the organ phase ran after Ctrl+C as the loop ended:\n%s", out)
	}
}

// A stop signal the loop's phase takes as the loop returns by itself, too late to stop it, goes on
// to the organ phase, which it skips: carried, it counts as one that came between the phases.
func TestAStopSignalAsTheLoopEndsSkipsTheOrgans(t *testing.T) {
	t.Parallel()
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGHUP} {
		r := newQuitRig(t, &windingLoop{})
		if !r.stopLoop(sig) {
			t.Fatal("the loop's phase didn't take the signal")
		}
		r.stops.on(nil) // the loop has returned, by itself
		r.stops.carry(sig)
		if out := organRun(t, r.stops); out != "" {
			t.Errorf("%v: the organ phase ran:\n%s", sig, out)
		}
		if len(r.exits) > 0 {
			t.Errorf("%v: quit %v", sig, r.exits)
		}
	}
}

// The answer talks reach the dashboard's program through the variable that holds it: it is set before
// the loop, which may run already, is given them (run under -race, which would find the race).
func TestTheDashboardsProgramComesBeforeTheLoopsAnswers(t *testing.T) {
	t.Parallel()
	var p *tea.Program
	send := func(tea.Msg) {
		if p == nil {
			t.Error("the answer talk sent to the dashboard before its program was set")
		}
	}
	var wg sync.WaitGroup
	openProgram(&p, tui.NewDashboard(dispatch.Config{}, func() {}, func(bool) {}, func(string) {}), func() {
		wg.Go(func() { send(tui.AnswerTaken{}) }) // the loop, running, opens the talk at once
	}, tea.WithInput(nil), tea.WithOutput(io.Discard))
	wg.Wait()
}

// hangingLoop is a stopped loop winding down whose saving of the workers it leaves hangs, as git can,
// until saved is closed.
type hangingLoop struct {
	windingLoop
	saving chan struct{} // closed once the saving has begun
	saved  chan struct{}
}

func (l *hangingLoop) SaveLeft() {
	close(l.saving)
	<-l.saved
}

// Quitting at once saves the workers left with git, outside the watch's lock: a further stop signal
// while that hangs is taken, and exits at once, logging that the workers weren't saved.
func TestASignalWhileQuittingHangsExitsAtOnce(t *testing.T) {
	t.Parallel()
	loop := &hangingLoop{windingLoop: windingLoop{running: []dispatch.Status{{Ticket: "a-1", Tab: "w1:2"}}},
		saving: make(chan struct{}), saved: make(chan struct{})}
	r := quitRigFor(t, &loop.windingLoop)
	exits := make(chan int, 2)
	q := r.quitter()
	q.loop, q.exit = loop, func(code int) { exits <- code }
	r.stops.quitWith(q)
	if !r.stopLoop(os.Interrupt) {
		t.Fatal("the loop didn't take the first signal")
	}
	go r.stops.deliver(os.Interrupt) // quits, and hangs saving the workers
	<-loop.saving
	took := make(chan struct{})
	go func() {
		r.stops.deliver(syscall.SIGTERM)
		close(took)
	}()
	select {
	case code := <-exits:
		if code != dispatch.ExitInterrupted {
			t.Errorf("exit %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the signal while quitting didn't exit: it waits on the saving")
	}
	<-took
	logged := r.logged(t)
	for _, want := range []string{
		"INTERRUPTED: quit at once with Ctrl+C, leaving a-1 (tab w1:2) running with its tab and worktree open",
		"INTERRUPTED: exited by SIGTERM before the workers left running were saved for the next run",
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("logged:\n%s\nwant %q", logged, want)
		}
	}
	r.stops.on(func(os.Signal) { t.Error("a phase listened while orchestra quit") })
	close(loop.saved)
	<-exits // the quitting's own exit, which a real one never reaches
}
