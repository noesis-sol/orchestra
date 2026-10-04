package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/herdr"
	"github.com/noesis-sol/orchestra/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// loopRun is what the two ways of running the loop, plain and under the dashboard, share: the loop
// and its configuration, the stop signals' watch, the log, and how to cancel the organs' context.
type loopRun struct {
	orch         *dispatch.Loop
	cfg          options
	stops        *stopWatch
	log          *dispatch.Log
	cancelOrgans func()
}

// organs runs the organ phase after the loop ended with code and its final line.
func (r loopRun) organs(code int, final string, out tui.Printer) {
	organPhase(r.orch, r.cfg, r.stops, r.log, code, final, out, r.cancelOrgans)
}

// runPlain runs the loop printing plain log lines (with --plain, or when stdout isn't a terminal),
// then the organ phase. It returns the loop's exit code.
func runPlain(ctx context.Context, r loopRun, stdout io.Writer) int {
	ctx, cancelRun := context.WithCancelCause(ctx)
	defer cancelRun(nil)
	sink := tui.Printer{Out: stdout}
	// The signal that stops the loop is the first; one while it winds down ends orchestra.
	r.stops.quitWith(quitter{loop: r.orch, log: r.log, out: sink, exit: os.Exit})
	r.stops.on(func(s os.Signal) { cancelRun(dispatch.InterruptedError(stoppedHow(s))) })
	stopDrain := watchDrain(func() { r.orch.Drain("by " + signalName(drainSignals[0])) })
	r.orch.SetSink(sink)
	r.orch.ReportInterrupt = true
	code := r.orch.Run(ctx)
	r.stops.on(nil)
	stopDrain()
	r.organs(code, r.orch.Final(), sink)
	return code
}

// runDashboard runs the loop under the interactive dashboard, then, with the terminal restored,
// prints the run's summary and the loop's events from where the dashboard left off, and runs the
// organ phase. It returns the loop's exit code, or ExitInterrupted when the dashboard closed before
// the loop ended.
func runDashboard(ctx context.Context, r loopRun, stdin io.Reader, stdout *os.File, stderr io.Writer) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// orchestra handles the signals itself: Bubble Tea's handler knows nothing of SIGHUP.
	drain := func(on bool) {
		if on {
			r.orch.Drain("from the dashboard")
		} else {
			r.orch.Resume("from the dashboard")
		}
	}
	// The dashboard draws on the alternate screen, which leaves the earlier output as it was and has
	// no scrollback: a terminal that reflows a frame as the pane resizes can't push its rows out of
	// Bubble Tea's reach, as it can on the normal screen.
	p := tea.NewProgram(tui.NewDashboard(r.cfg.Config, cancel, drain, focusTab(ctx, r.log)),
		tea.WithInput(stdin), tea.WithOutput(stdout), tea.WithAltScreen(), tea.WithoutSignalHandler())
	quitBy := make(chan os.Signal, 1)
	r.stops.on(func(s os.Signal) {
		quitBy <- s // before the quit, so stoppedBy finds it
		go p.Quit() // Quit waits for the dashboard to have started; a stop mustn't wait
	})
	// the dashboard hears it from the loop
	stopDrain := watchDrain(func() { r.orch.Drain("by " + signalName(drainSignals[0])) })
	progSink := tui.NewProgramSink(p)
	r.orch.SetSink(progSink)
	codes := make(chan int, 1)
	ran := make(chan struct{}) // closed once the dashboard has exited and the terminal is restored
	go func() {
		// The loop recovers its workers' panics; one in the loop itself is a bug that ends
		// orchestra, but not with the terminal left in raw mode. Panicking again from here keeps
		// the original stack in the crash.
		defer func() {
			if v := recover(); v != nil {
				r.log.Line(time.Now(), fmt.Sprintf("PANIC: %v\n\n%s", v, debug.Stack()))
				p.Kill()
				<-ran
				panic(v)
			}
		}()
		codes <- r.orch.Run(ctx)
		p.Send(tui.Finished{})
	}()
	final, err := p.Run()
	close(ran)
	// A stop signal from here on goes to the organ phase, which it skips, until quitWith below: one
	// that came while the dashboard closed was a further one, if a signal closed it.
	r.stops.on(nil)
	var sig os.Signal // the one that closed the dashboard, if any
	select {
	case sig = <-quitBy:
	default:
	}
	stopDrain()
	if err != nil {
		fmt.Fprintln(stderr, "orchestra:", err)
	}
	// As wide as the pane is now: it may have narrowed under the dashboard.
	sink := tui.Printer{Out: stdout, Styled: true, Width: termWidth(stdout)}
	m, _ := final.(tui.Dashboard)
	sink.Summary(m) // the dashboard went with the alternate screen
	sink.End(m)
	// From here the loop's events are printed, starting with any the dashboard never received.
	progSink.Handoff(sink, m.Received())
	// With the terminal restored, a stop signal while the stopped loop winds down can end orchestra.
	r.stops.quitWith(quitter{loop: r.orch, log: r.log, out: sink, exit: os.Exit})
	if why := stoppedBy(m, err, len(codes) > 0, sig); why != "" {
		// The loop may be in the middle of a command; log the stop and leave the workers to the user.
		cancel()
		msg := dispatch.InterruptLine(why, r.orch.Running())
		ev := dispatch.Event{Kind: dispatch.EvStop, Detail: dispatch.Interrupted, Text: msg, Time: time.Now()}
		r.log.Line(ev.Time, msg)
		r.log.Notify(dispatch.Notice(ev))
		r.log.Record(ev)
		sink.Event(ev)
		r.stops.windDown()
		// Let the loop and its workers stop before triage closes and the reviewer reads its state.
		// Run waits for its workers, whose commands stop with it or at their time limits, and names
		// any not back within a second below the INTERRUPTED line.
		<-codes
		r.organs(dispatch.ExitInterrupted, msg, sink)
		return dispatch.ExitInterrupted
	}
	code := <-codes
	r.organs(code, r.orch.Final(), sink)
	return code
}

// focusTab is the dashboard's callback for a worker's number: it switches Herdr to the worker's tab
// in the background, as the dashboard mustn't wait on Herdr. A failure goes to the log and changes
// nothing on screen.
func focusTab(ctx context.Context, log *dispatch.Log) func(tab string) {
	return func(tab string) {
		go func() {
			if err := (herdr.Terminal{}).FocusTab(ctx, tab); err != nil {
				log.Raw("", fmt.Errorf("cannot switch to tab %s from the dashboard: %w", tab, err))
			}
		}()
	}
}

// stoppedBy says what stopped the run when the dashboard m has closed before the loop ended by
// itself: Ctrl+C in the dashboard, a signal (sig, nil if none came) that closed it, or the
// dashboard failing (err is what its program returned) while the loop was still running. It is ""
// when the loop ended the run.
func stoppedBy(m tui.Dashboard, err error, loopDone bool, sig os.Signal) string {
	switch {
	case m.Interrupted():
		return "with Ctrl+C"
	case m.Final() != nil || loopDone:
		return ""
	case sig != nil:
		return "by " + signalName(sig)
	case err != nil:
		return "because the dashboard failed"
	default:
		// The dashboard quits by itself only on the loop's last event, so this shouldn't happen.
		return "because the dashboard closed"
	}
}
