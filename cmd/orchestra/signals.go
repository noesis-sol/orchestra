package main

import (
	"context"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

// stopSignals stop a run the way Ctrl+C does: SIGINT, kill's SIGTERM, and SIGHUP from closing the
// terminal or Herdr pane.
var stopSignals = []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}

// stopWatch catches stopSignals for the whole of a run, so that none of them ends orchestra
// outright: a merge under way, its notes and the log would be left unfinished. Each phase of the
// run (the feature request, the loop, the organs) says with on what a signal does to it. A signal
// that comes while no phase listens, between phases or while a stopped loop winds down, goes to
// the next phase.
type stopWatch struct {
	sigs chan os.Signal
	done chan struct{}

	mu      sync.Mutex
	onStop  func(os.Signal) // the current phase's, for the next signal; nil while none listens
	pending os.Signal       // a signal no phase has taken yet
	leave   bool            // SIGTERM or SIGHUP has come: see leaving
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
		w.onStop = nil
		f(s)
	case w.pending == nil:
		w.pending = s
	}
}

// on makes f what the next stop signal does; with nil, the signal waits for the next phase. f is
// called at most once: at once if a signal is waiting, otherwise from another goroutine when one
// comes. It must not block or use w. Once on returns, a call of the f it replaces has finished.
func (w *stopWatch) on(f func(os.Signal)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if f != nil && w.pending != nil {
		s := w.pending
		w.pending, w.onStop = nil, nil
		f(s)
		return
	}
	w.onStop = f
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
