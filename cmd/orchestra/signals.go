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

// stopWatch catches stopSignals for the whole of a run, so that the first of them doesn't end
// orchestra outright: a merge under way, its notes and the log would be left unfinished. Each
// phase of the run (the feature request, the loop, the organs) says with on what a signal does to
// it. A signal that comes while no phase listens, between phases, goes to the next phase.
//
// One that comes while a phase it stopped winds down ends orchestra instead, once quitWith has
// said how, so that a shutdown that hangs can be left: see further. Before that (the feature
// request, and the loop while the dashboard is open) it too goes to the next phase.
type stopWatch struct {
	sigs chan os.Signal
	done chan struct{}

	mu      sync.Mutex
	onStop  func(os.Signal) // the current phase's, for the next signal; nil while none listens
	pending os.Signal       // a signal no phase has taken yet
	leave   bool            // SIGTERM or SIGHUP has come: see leaving
	quit    *quitter        // how a further signal ends orchestra; nil until quitWith
	winding bool            // the phase listening last has taken its signal and winds down
	warned  bool            // a further signal has said what is under way: the next one quits
}

// quitter is what a further stop signal needs to end orchestra: the loop, to say which of its
// workers are left running and what is under way that a stop doesn't cut short, the log, where to
// show the lines, and how to exit.
type quitter struct {
	loop interface {
		Running() []dispatch.Status
		Finishing() []dispatch.Finishing
	}
	log  *dispatch.Log
	out  tui.Printer
	exit func(code int) // os.Exit
}

// catchStops starts catching stopSignals. Call release once the run is over.
func catchStops() *stopWatch {
	w := &stopWatch{sigs: make(chan os.Signal, len(stopSignals)), done: make(chan struct{})}
	signal.Notify(w.sigs, stopSignals...)
	go func() {
		for {
			select {
			case s := <-w.sigs:
				w.deliver(s)
			case <-w.done:
				return
			}
		}
	}()
	return w
}

func (w *stopWatch) deliver(s os.Signal) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.leave = w.leave || leaves(s)
	switch {
	case w.onStop != nil:
		f := w.onStop
		w.onStop, w.winding = nil, true
		f(s)
	case w.winding && w.quit != nil:
		w.further(s)
	case w.pending == nil:
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
	w.winding, w.warned = false, false
	if f != nil && w.pending != nil {
		s := w.pending
		w.pending, w.onStop, w.winding = nil, nil, true
		f(s)
		return
	}
	w.onStop = f
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
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onStop, w.winding = nil, true
	if s := w.pending; s != nil && w.quit != nil {
		w.pending = nil
		w.further(s)
	}
}

// further handles stop signal s, which came while a phase it stopped winds down. Nothing under
// way that a stop doesn't cut short (a merge, a worktree being set up), it ends orchestra at once,
// exit code 130: it logs and shows a line naming the workers left running, and closes the log.
// Otherwise the first says what is under way and how to abandon it, and waits for the next phase,
// skipping the organs, as before; the one after it ends orchestra, naming what was abandoned.
// Commands under way run in their own process groups, so they run on after orchestra has gone.
// The caller holds w.mu.
func (w *stopWatch) further(s os.Signal) {
	q := w.quit
	under := q.loop.Finishing()
	if len(under) > 0 && !w.warned {
		w.warned = true
		if w.pending == nil {
			w.pending = s
		}
		again := "send " + signalName(s) + " again"
		if s == os.Interrupt {
			again = "press Ctrl+C again"
		}
		ev := dispatch.Event{Kind: dispatch.EvWarn, Text: dispatch.UnderWayLine(under, again), Time: time.Now()}
		q.log.Line(ev.Time, ev.Text)
		q.out.Event(ev)
		return
	}
	ev := dispatch.Event{Kind: dispatch.EvStop, Text: dispatch.QuitLine(stoppedHow(s), q.loop.Running(), under),
		Time: time.Now()}
	q.log.Line(ev.Time, ev.Text) // not as a notification: orchestra doesn't wait for one now
	q.out.Event(ev)
	_ = q.log.CloseNow() // each line was written as it came: nothing is left to lose
	q.exit(dispatch.ExitInterrupted)
}

// context returns a copy of parent that the next stop signal cancels, as signal.NotifyContext does,
// and the function that cancels it and stops listening.
func (w *stopWatch) context(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	w.on(func(os.Signal) { cancel() })
	return ctx, func() {
		w.on(nil)
		cancel()
	}
}

// leaving reports whether SIGTERM or SIGHUP has come during the run: orchestra is asked to go
// away, or nobody is left to read the report.
func (w *stopWatch) leaving() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.leave
}

// release stops catching stopSignals: from then on they have their default effect again.
func (w *stopWatch) release() {
	signal.Stop(w.sigs)
	close(w.done)
}

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

// watchDrain calls onDrain, from another goroutine, each time orchestra receives one of
// drainSignals. The function it returns stops watching.
func watchDrain(onDrain func()) (stop func()) {
	if len(drainSignals) == 0 {
		return func() {} // signal.Notify with no signals would catch them all
	}
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, drainSignals...)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-sigs:
				onDrain()
			case <-done:
				return
			}
		}
	}()
	return func() {
		signal.Stop(sigs)
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
