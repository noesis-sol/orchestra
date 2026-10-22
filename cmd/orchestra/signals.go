package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// stopSignals stop a run the way Ctrl+C does: SIGINT, kill's SIGTERM, and SIGHUP from closing the
// terminal or Herdr pane.
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// catchStop starts catching stopSignals for a subcommand whose policy is stopCancels (see
// stopPolicy): the first of them cancels stop.ctx, a copy of parent, and stop.stopped reports whether
// one has, rather than parent ending. release stops catching them.
func catchStop(parent context.Context) (stop stopCatch, release func()) {
	ctx, cancel := signal.NotifyContext(parent, stopSignals...)
	return stopCatch{ctx: ctx, stopped: func() bool { return ctx.Err() != nil && parent.Err() == nil }}, cancel
}

// pipeSignals are what a write to stdout or stderr gets once nothing reads it, as with
// orchestra --plain | tee and Ctrl+C, which ends tee too: SIGPIPE. Go's default ends orchestra at that
// write, in the middle of winding down; caught, the write fails with EPIPE instead (and the printer
// drops it), and the run ends as it would have. Caught rather than ignored (signal.Ignore), as a
// program orchestra starts would inherit the ignoring.
var pipeSignals = []os.Signal{syscall.SIGPIPE}

// stopWatch catches stopSignals for the whole of a run, so that the first of them doesn't end
// orchestra outright: a merge under way, its notes and the log would be left unfinished. Each
// phase of the run (the feature request, the loop, the organs) says with on what a signal does to
// it. A signal that comes while no phase listens, between phases, goes to the next phase.
//
// One that comes while a phase it stopped winds down ends orchestra instead, once quitWith has
// said how, so that a shutdown that hangs can be left: see further. Before that (the feature
// request, and the loop while the dashboard is open) it too goes to the next phase.
//
// What a stop signal does follows from the watch's phase:
//
//	between                                    → it pends (the first is kept)
//	listening                                  → onStop gets it → winding
//	winding, no quitter                        → it pends
//	winding, quitter set, something finishing  → a warning, it pends → warned
//	winding, quitter set, nothing finishing    → orchestra quits, exit code 130 → quitting
//	warned                                     → orchestra quits, exit code 130 → quitting
//	quitting                                   → orchestra exits at once, exit code 130
//
// on(f) starts the next phase: listening with an f, between without one (or winding at once, when
// f takes a signal that pends). windDown goes to winding without a signal. Quitting is the last
// phase: nothing leaves it.
//
// The watch decides under its lock and does the work a signal calls for (the warning, the quitting,
// which saves the workers left with git) outside it, beside the goroutine that takes the signals: a
// further signal while the quitting hangs is taken, and exits.
type stopWatch struct {
	stop func() // stops the relays: see release

	mu      sync.Mutex
	phase   stopPhase
	onStop  func(os.Signal) // the listening phase's, for the next signal
	pending os.Signal       // a signal no phase has taken yet
	dropInt bool            // SIGINT is the terminal program's: see dropInterrupts
	leave   bool            // SIGTERM or SIGHUP has come: see leaving
	quit    *quitter        // how a further signal ends orchestra; nil until quitWith
}

// stopPhase is where a stopWatch is in a run, which decides what the next stop signal does: see
// stopWatch.
type stopPhase int

const (
	between   stopPhase = iota // no phase listens: a signal waits for the next
	listening                  // the current phase's onStop takes the next signal
	winding                    // the phase listening last has taken its signal and winds down
	warned                     // a further signal has said what is under way: the next one quits
	quitting                   // a further signal is ending orchestra: the next one exits at once
)

// quitter is what a further stop signal needs to end orchestra: the loop, to say which of its
// workers are left running and what is under way that a stop doesn't cut short, and to save the
// workers it leaves for the next run, the log, where to show the lines, and how to exit.
type quitter struct {
	loop interface {
		Running() []dispatch.Status
		Finishing() []dispatch.Finishing
		SaveLeft()
	}
	log  *dispatch.Log
	out  tui.Printer
	exit func(code int) // os.Exit
}

// catchStops starts catching stopSignals, and pipeSignals, which it drops. Call release once the
// run is over. The work a signal calls for runs in a goroutine of its own, so that the next signal
// is taken while it goes on.
func catchStops() *stopWatch {
	w := &stopWatch{}
	stopPipes := relay(pipeSignals, func(os.Signal) {})
	stopStops := relay(stopSignals, func(s os.Signal) {
		if act := w.take(s); act != nil {
			go act()
		}
	})
	w.stop = func() {
		stopStops()
		stopPipes()
	}
	return w
}

// deliver handles stop signal s, as a caught one is handled, and returns once the work it calls for
// is done.
func (w *stopWatch) deliver(s os.Signal) {
	if act := w.take(s); act != nil {
		act()
	}
}

// take handles stop signal s under the watch's lock, and returns the work it calls for, to be done
// outside it, if any.
func (w *stopWatch) take(s os.Signal) (act func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if s == os.Interrupt && w.dropInt {
		return nil
	}
	w.leave = w.leave || leaves(s)
	switch w.phase {
	case listening:
		f := w.onStop
		w.onStop, w.phase = nil, winding
		f(s)
	case winding, warned:
		if w.quit != nil {
			return w.further(s)
		}
		w.pend(s)
	case quitting:
		return w.quit.exitNow(s)
	case between:
		w.pend(s)
	}
	return nil
}

// pend keeps s for the next phase, unless a signal already waits. The caller holds w.mu.
func (w *stopWatch) pend(s os.Signal) {
	if w.pending == nil {
		w.pending = s
	}
}

// on makes f what the next stop signal does; with nil, the signal waits for the next phase. f is
// called at most once: at once if a signal is waiting, otherwise from another goroutine when one
// comes. It must not block or use w. Once on returns, a call of the f it replaces has finished.
// It ends the winding down of the phase before, if any: on means that phase is over.
func (w *stopWatch) on(f func(os.Signal)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.phase == quitting { // orchestra is ending: a further signal exits at once
		return
	}
	if f != nil && w.pending != nil {
		s := w.pending
		w.pending, w.onStop, w.phase = nil, nil, winding
		f(s)
		return
	}
	w.onStop, w.phase = f, between
	if f != nil {
		w.phase = listening
	}
}

// carry keeps s, a stop signal a phase took too late to stop, for the next phase, which it stops at
// once, as one that came between phases does: a stop asked as the loop ended stops the organs.
func (w *stopWatch) carry(s os.Signal) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.phase != quitting {
		w.pend(s)
	}
}

// dropInterrupts drops SIGINT until the function it returns is called: it neither stops the
// current phase nor waits for the next. It is for a program orchestra runs on the terminal in
// orchestra's own process group (command.Interactive), which gets Ctrl+C from the terminal too:
// Claude Code interrupts and clears with it. SIGTERM and SIGHUP still stop the phase. SIGINT stays
// caught rather than ignored (signal.Ignore), as a program started meanwhile would inherit the
// ignoring.
func (w *stopWatch) dropInterrupts() (restore func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dropInt = true
	return func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.dropInt = false
	}
}

// quitWith has the stop signals that come while a stopped phase winds down end orchestra, as q
// says, rather than wait for the next phase.
func (w *stopWatch) quitWith(q quitter) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.quit = &q
}

// windDown says the loop was stopped by other means than a stop signal, such as Ctrl+C in the
// dashboard, and winds down: it stops listening, and the signals from now until the next phase
// listens are further ones. A signal waiting for the next phase counts as the first of them.
func (w *stopWatch) windDown() {
	if act := w.windingDown(); act != nil {
		act()
	}
}

// windingDown is windDown under the watch's lock: it returns the work a signal waiting calls for, to
// be done outside it, if any.
func (w *stopWatch) windingDown() (act func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.phase == quitting {
		return nil
	}
	w.onStop = nil
	if w.phase != warned {
		w.phase = winding
	}
	if s := w.pending; s != nil && w.quit != nil {
		w.pending = nil
		return w.further(s)
	}
	return nil
}

// further handles stop signal s, which came while a phase it stopped winds down. Nothing under
// way that a stop doesn't cut short (a merge, a worktree being set up), it ends orchestra at once,
// exit code 130: it saves the workers the loop leaves for the next run, as the loop would have as it
// ended (see dispatch.Loop.SaveLeft: their tickets aren't labelled, which would wait on bd), logs
// and shows a line naming the workers left running, and closes the log.
// Otherwise the first says what is under way and how to abandon it, and waits for the next phase,
// skipping the organs, as before; the one after it ends orchestra, naming what was abandoned.
// Commands under way run in their own process groups, so they run on after orchestra has gone.
// The caller holds w.mu; further returns the work, which the caller does once it has let go of it.
func (w *stopWatch) further(s os.Signal) (act func()) {
	q := w.quit
	under := q.loop.Finishing()
	if len(under) > 0 && w.phase != warned {
		w.phase = warned
		w.pend(s)
		return func() { q.warn(s, under) }
	}
	w.phase = quitting
	return func() { q.quitNow(s, under) }
}

// warn says that under is under way, which signal s, a further one, doesn't cut short, and how to
// abandon it.
func (q *quitter) warn(s os.Signal, under []dispatch.Finishing) {
	again := "send " + signalName(s) + " again"
	if s == os.Interrupt {
		again = "press Ctrl+C again"
	}
	ev := dispatch.Event{Kind: dispatch.EvWarn, Text: dispatch.UnderWayLine(under, again), Time: time.Now()}
	q.log.Line(ev.Time, ev.Text)
	q.log.Record(ev)
	q.out.Event(ev)
}

// quitNow ends orchestra, exit code 130, as further signal s asks, abandoning under. The line comes
// first, so that it is there should saving the workers hang and a further signal exit at once.
func (q *quitter) quitNow(s os.Signal, under []dispatch.Finishing) {
	ev := dispatch.Event{Kind: dispatch.EvStop, Detail: dispatch.Interrupted,
		Text: dispatch.QuitLine(stoppedHow(s), q.loop.Running(), under), Time: time.Now()}
	q.log.Line(ev.Time, ev.Text) // not as a notification: orchestra doesn't wait for one now
	q.log.Record(ev)
	q.out.Event(ev)
	q.loop.SaveLeft()                   // the loop's own saving, as it ends, never comes
	q.log.End(dispatch.ExitInterrupted) // run's deferred End doesn't run: orchestra exits from here
	_ = q.log.CloseNow()                // each line was written as it came: nothing is left to lose
	q.exit(dispatch.ExitInterrupted)
}

// exitNow returns what signal s does while orchestra quits: it exits at once, exit code 130, without
// waiting for the quitting, which hangs, to save the workers left running for the next run.
func (q *quitter) exitNow(s os.Signal) (act func()) {
	return func() {
		now := time.Now()
		q.log.Line(now, "INTERRUPTED: exited "+stoppedHow(s)+" before the workers left running were saved "+
			"for the next run")
		q.log.End(dispatch.ExitInterrupted)
		_ = q.log.CloseNow() // as in quitNow
		q.exit(dispatch.ExitInterrupted)
	}
}

// context returns a copy of parent that the next stop signal cancels, as signal.NotifyContext does,
// and the function that stops listening and cancels it, which reports whether a stop signal did first.
func (w *stopWatch) context(parent context.Context) (context.Context, func() (stopped bool)) {
	ctx, cancel := context.WithCancel(parent)
	w.on(func(os.Signal) { cancel() })
	return ctx, func() bool {
		w.on(nil)
		stopped := ctx.Err() != nil
		cancel()
		return stopped
	}
}

// leaving reports whether SIGTERM or SIGHUP has come during the run: orchestra is asked to go
// away, or nobody is left to read the report.
func (w *stopWatch) leaving() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.leave
}

// release stops catching stopSignals and pipeSignals: from then on they have their default effect again.
func (w *stopWatch) release() { w.stop() }

// stoppedHow says how signal s stopped the run, for its INTERRUPTED line: "with Ctrl+C", "by SIGTERM".
func stoppedHow(s os.Signal) string {
	if s == os.Interrupt {
		return "with Ctrl+C"
	}
	return "by " + signalName(s)
}

// leaves reports whether sig asks orchestra to go away rather than stop the run: SIGTERM, or
// SIGHUP when nobody is left to read the report. The organ phase is skipped then.
func leaves(sig os.Signal) bool { return sig == syscall.SIGTERM || sig == syscall.SIGHUP }

// drainWatch catches drainSignals for the whole of a run, from its start, so that one that comes
// before the loop listens (during the overnight pre-flight, the start question, or a spec's or a
// feature's talk) isn't lost: Go ignores a SIGUSR1 nobody catches. One that comes while nothing
// listens waits for the loop, which drains as soon as it begins (to).
type drainWatch struct {
	stop func() // stops the relay: see release

	mu      sync.Mutex
	onDrain func() // what a drain signal does now; nil while no loop listens
	pending bool   // a drain signal came while none did
}

// catchDrains starts catching drainSignals. Call release once the run is over.
func catchDrains() *drainWatch {
	w := &drainWatch{}
	w.stop = relay(drainSignals, func(os.Signal) { w.deliver() })
	return w
}

func (w *drainWatch) deliver() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.onDrain == nil {
		w.pending = true
		return
	}
	w.onDrain()
}

// to makes f what each drain signal does, from another goroutine, and calls it at once for one that
// came while nothing listened; with nil, the signals wait for the next f. f must not block.
func (w *drainWatch) to(f func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onDrain = f
	if f != nil && w.pending {
		w.pending = false
		f()
	}
}

// release stops catching drainSignals: from then on Go ignores them again.
func (w *drainWatch) release() { w.stop() }

// relay catches sigs and calls fn with each, in turn, from another goroutine. The function it
// returns stops catching them: from then on they have their default effect. With no sigs it
// catches none.
func relay(sigs []os.Signal, fn func(os.Signal)) (stop func()) {
	if len(sigs) == 0 {
		return func() {} // signal.Notify with no signals would catch them all
	}
	ch := make(chan os.Signal, len(sigs))
	signal.Notify(ch, sigs...)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-ch:
				fn(s)
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(ch)
		close(done)
	}
}

func signalName(s os.Signal) string {
	switch s {
	case os.Interrupt:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGHUP:
		return "SIGHUP"
	}
	if len(drainSignals) > 0 && s == drainSignals[0] {
		return "SIGUSR1"
	}
	return s.String()
}
