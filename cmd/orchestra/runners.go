package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"github.com/noesis-sol/orchestra/internal/beads"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"

	tea "github.com/charmbracelet/bubbletea"
)

// loopRun is what the two ways of running the loop, plain and under the dashboard, share: the loop
// and its configuration, the stop and drain signals' watches, the log, the context of the organ
// phase, and how to cancel the organs' context.
type loopRun struct {
	orch         *dispatch.Loop
	cfg          options
	stops        *stopWatch
	drains       *drainWatch
	log          *dispatch.Log
	base         context.Context // the run's, without its cancellation
	cancelOrgans func()
	filing       *specFiling // the spec's breakdown going on as the loop starts; nil for none
}

// organs runs the organ phase after the loop ended with code and its final line, and reports whether
// a stop signal skipped some of it.
func (r loopRun) organs(code int, final string, out tui.Printer) bool {
	return organPhase(r.base, r.orch, r.cfg, r.stops, r.log, code, final, out, r.cancelOrgans)
}

// drainLoop has each drain signal, and one that came before the loop, ask the loop to wind down. The
// function it returns stops that, keeping later ones from a loop that has ended.
func (r loopRun) drainLoop() (stop func()) {
	r.drains.to(func() { r.orch.Drain("by " + signalName(drainSignals[0])) })
	return func() { r.drains.to(nil) }
}

// runPlain runs the loop printing plain log lines (with --plain, or when stdout isn't a terminal),
// then the organ phase. It returns the loop's exit code.
func runPlain(ctx context.Context, r loopRun, stdout io.Writer) int {
	ctx, cancelRun := context.WithCancelCause(ctx)
	defer cancelRun(nil)
	sink := tui.Printer{Out: stdout}
	// The signal that stops the loop is the first; one while it winds down ends orchestra.
	r.stops.quitWith(quitter{loop: r.orch, log: r.log, out: sink, exit: os.Exit})
	took := make(chan os.Signal, 1)
	r.stops.on(func(s os.Signal) {
		took <- s
		cancelRun(dispatch.InterruptedError(stoppedHow(s)))
		r.filing.stopBreakdown()
	})
	defer r.filing.bind(cancelRun)()
	stopDrain := r.drainLoop()
	r.orch.SetSink(sink)
	r.orch.SetAnswers(runAnswers{tracker: beads.Tracker{Repo: r.cfg.Repo}, scope: r.cfg.Ticket}) // no talk: plain
	r.orch.ReportInterrupt = true
	ran := r.orch.Run(ctx)
	code := r.filing.result(ran)
	r.stops.on(nil)
	stopDrain()
	// A signal the loop took as it ended by itself, too late to stop it, stops the organ phase instead.
	select {
	case s := <-took:
		if ran != dispatch.ExitInterrupted {
			r.stops.carry(s)
		}
	default:
	}
	r.organs(code, r.orch.Final(), sink)
	return code
}

// runDashboard runs the loop under the interactive dashboard, then, with the terminal restored,
// prints the run's summary and the loop's events from where the dashboard left off, and runs the
// organ phase. It returns the loop's exit code, or, when the dashboard closed before the loop ended,
// that of what closed it (see stoppedBy).
func runDashboard(ctx context.Context, r loopRun, stdin io.Reader, stdout *os.File, stderr io.Writer) int {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	return dashboard(ctx, cancel, r, nil, stdin, stdout, stderr)
}

// startedLoop is a loop already running as the dashboard opens, its events held for it: a spec's
// breakdown went on as the loop started (runFiling).
type startedLoop struct {
	codes <-chan int // the loop's exit code, once Run returns
	held  *heldSink
	began time.Time // when the loop started
	// onPanic is what the loop panicking does before it ends orchestra: once the dashboard is open, it
	// closes it, so that the terminal isn't left in raw mode.
	onPanic atomic.Pointer[func()]
}

// dashboard is runDashboard once ctx, which cancel cancels, is the loop's: it starts the loop, unless
// started has it running already.
func dashboard(ctx context.Context, cancel func(), r loopRun, started *startedLoop, stdin io.Reader,
	stdout *os.File, stderr io.Writer) int {
	// orchestra handles the signals itself: Bubble Tea's handler knows nothing of SIGHUP.
	drain := func(on bool) {
		if on {
			r.orch.Drain("from the dashboard")
		} else {
			r.orch.Undrain("from the dashboard")
		}
	}
	// The dashboard draws on the alternate screen, which leaves the earlier output as it was and has
	// no scrollback: a terminal that reflows a frame as the pane resizes can't push its rows out of
	// Bubble Tea's reach, as it can on the normal screen.
	var focus func(tab string) // none when the workers' tabs are out of orchestra's sight
	if r.cfg.where.inSight {
		focus = focusTab(ctx, r.log, terminalOf(r.cfg))
	}
	d := tui.NewDashboard(r.cfg.Config, cancel, drain, focus)
	if started != nil {
		d = d.StartedAt(started.began)
	}
	var p *tea.Program
	var answers *answerTalks // none where orchestra runs in no pane, which the talk opens beside (answer_dashboard.go)
	if r.cfg.where.pane != "" {
		answers = newAnswerTalks(ctx, dashboardAnswerTalk(r.cfg, r.log), r.cfg.Ticket, r.log,
			func(msg tea.Msg) { p.Send(msg) })
		d = d.WithAnswer(answers.callbacks(), talksByItself(r.cfg))
	}
	idle := runAnswers{tracker: beads.Tracker{Repo: r.cfg.Repo}, scope: r.cfg.Ticket}
	if talksWhenIdle(r.cfg, false) {
		idle.talks = answers
	}
	openProgram(&p, d, func() { r.orch.SetAnswers(idle) },
		tui.WithTerminal(stdin, stdout), tea.WithAltScreen(), tea.WithoutSignalHandler())
	quitBy := make(chan os.Signal, 1)
	r.stops.on(func(s os.Signal) {
		quitBy <- s // before the quit, so stoppedBy finds it
		go p.Quit() // Quit waits for the dashboard to have started; a stop mustn't wait
	})
	stopDrain := r.drainLoop() // the dashboard hears it from the loop
	progSink := tui.NewProgramSink(p)
	codes := make(chan int, 1)
	ran := make(chan struct{}) // closed once the dashboard has exited and the terminal is restored
	if started == nil {
		r.orch.SetSink(progSink)
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
	} else {
		kill := func() {
			p.Kill()
			<-ran
		}
		started.onPanic.Store(&kill)
		go func() {
			started.held.handTo(progSink) // its sends wait for the dashboard to start, and come before its end
			codes <- <-started.codes
			p.Send(tui.Finished{})
		}()
	}
	final, err := p.Run()
	close(ran)
	answers.dashboardClosed()
	// A stop signal from here on goes to the organ phase, which it skips, until quitWith below: one
	// that came while the dashboard closed was a further one, if a signal closed it.
	r.stops.on(nil)
	var sig os.Signal // the one that closed the dashboard, if any
	select {
	case sig = <-quitBy:
	default:
	}
	stopDrain()
	code, loopDone := 0, false
	select {
	case code = <-codes:
		loopDone = true
	default:
	}
	if err != nil {
		fmt.Fprintln(stderr, "orchestra:", err)
	}
	// As wide as the pane is now: it may have narrowed under the dashboard.
	sink := tui.Terminal(stdout, termWidth(stdout))
	m, _ := final.(tui.Dashboard)
	sink.Summary(m) // the dashboard went with the alternate screen
	sink.End(m)
	// From here the loop's events are printed, starting with any the dashboard never received.
	progSink.Handoff(sink, m.Received())
	// With the terminal restored, a stop signal while the stopped loop winds down can end orchestra.
	r.stops.quitWith(quitter{loop: r.orch, log: r.log, out: sink, exit: os.Exit})
	ended := loopEnded(m, loopDone, code)
	if stop := stoppedBy(m, err, ended, sig); stop.why != "" {
		// The loop may be in the middle of a command; log the stop and leave the workers to the user.
		cancel()
		ev := stop.event(r.orch.Running())
		r.log.Line(ev.Time, ev.Text)
		r.log.Notify(dispatch.Notice(ev))
		r.log.Record(ev)
		sink.Event(ev)
		r.stops.windDown()
		// Let the loop and its workers stop before triage closes and the reviewer reads its state.
		// Run waits for its workers, whose commands stop with it or at their time limits, and names
		// any not back within a second below the INTERRUPTED line.
		if !loopDone {
			<-codes
		}
		stopped := r.organs(stop.code, ev.Text, sink)
		answers.finish(r.stops, stopped, stdout) // closed as the run stopped
		return stop.code
	}
	if !loopDone {
		code = <-codes
	}
	if sig != nil || m.Interrupted() { // a stop asked as the loop ended, too late to stop it, stops the organs
		r.stops.carry(signalOr(sig, os.Interrupt))
	}
	stopped := r.organs(code, r.orch.Final(), sink)
	answers.finish(r.stops, stopped, stdout)
	return code
}

// focusTab is the dashboard's callback for a worker's number: it switches the terminal to the
// worker's tab in the background, as the dashboard mustn't wait on it. A failure goes to the log and
// changes nothing on screen.
func focusTab(ctx context.Context, log *dispatch.Log, terminal interface {
	FocusTab(ctx context.Context, tab string) error
}) func(tab string) {
	return func(tab string) {
		go func() {
			if err := terminal.FocusTab(ctx, tab); err != nil {
				log.Raw("", fmt.Errorf("cannot switch to tab %s from the dashboard: %w", tab, err))
			}
		}()
	}
}

// openProgram makes the dashboard's program for m in *p, and only then calls publish, which hands
// the loop what reaches the program through *p (the answer talks): the loop may be running already
// (runFiling), and reach it at once.
func openProgram(p **tea.Program, m tea.Model, publish func(), opts ...tea.ProgramOption) {
	*p = tea.NewProgram(m, opts...)
	publish()
}

// loopEnded reports whether the loop ended the run by itself as the dashboard m closed: m shows its
// last event, or it returned (loopDone) with code other than the one a stop gives it, or without
// Ctrl+C in the dashboard having stopped it.
func loopEnded(m tui.Dashboard, loopDone bool, code int) bool {
	return m.Final() != nil || loopDone && (code != dispatch.ExitInterrupted || !m.Interrupted())
}

// earlyStop is what stopped the run as the dashboard closed before the loop ended: why, in the words
// of its line ("with Ctrl+C"), and the exit code.
type earlyStop struct {
	why  string
	code int
	err  error // the dashboard's failure, for its line; nil for a stop asked
}

// event is the line that says the run stopped, naming the workers left running.
func (s earlyStop) event(running []dispatch.Status) dispatch.Event {
	ev := dispatch.Event{Kind: dispatch.EvStop, Detail: dispatch.Interrupted,
		Text: dispatch.InterruptLine(s.why, running), Time: time.Now()}
	if s.err != nil {
		ev.Detail = dispatch.TerminalFailed
		ev.Text = dispatch.TerminalFailed + ": " + strings.TrimPrefix(ev.Text, dispatch.Interrupted+": ")
	}
	return ev
}

// stoppedBy says what stopped the run when the dashboard m has closed before the loop ended by
// itself: Ctrl+C in the dashboard, a signal (sig, nil if none came) that closed it, or the
// dashboard failing (err is what its program returned) while the loop was still running. Its why
// is "" when the loop ended the run (ended), whatever came as it did. A failed dashboard ends the run
// as a terminal failure, exit code 4, not 130, which is a stop asked for.
func stoppedBy(m tui.Dashboard, err error, ended bool, sig os.Signal) earlyStop {
	switch {
	case ended:
		return earlyStop{code: dispatch.ExitOK}
	case m.Interrupted():
		return earlyStop{why: "with Ctrl+C", code: dispatch.ExitInterrupted}
	case sig != nil:
		return earlyStop{why: "by " + signalName(sig), code: dispatch.ExitInterrupted}
	case err != nil:
		return earlyStop{why: "because the dashboard failed (" + dispatch.FirstLine(err.Error()) + ")",
			code: dispatch.ExitTool, err: err}
	default:
		// The dashboard quits by itself only on the loop's last event, so this shouldn't happen.
		return earlyStop{why: "because the dashboard closed", code: dispatch.ExitInterrupted}
	}
}

// signalOr is s, or or where s is nil.
func signalOr(s, or os.Signal) os.Signal {
	if s != nil {
		return s
	}
	return or
}
