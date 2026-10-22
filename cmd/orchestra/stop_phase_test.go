package main

import (
	"os"
	"syscall"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A stop signal does what stopWatch's phase table says: from each phase, with or without a
// quitter and with or without something finishing, it pends, goes to the listening phase, warns
// or quits, and leaves the watch in the phase the table names.
func TestStopPhaseTable(t *testing.T) {
	t.Parallel()
	merge := []dispatch.Finishing{{Ticket: "a-1", What: "merge"}}
	for _, tc := range []struct {
		name      string
		phase     stopPhase
		quit      bool
		finishing []dispatch.Finishing
		want      stopPhase
		taken     bool // the listening phase's onStop got the signal
		pends     bool
		quits     bool
	}{
		{name: "between", phase: between, want: between, pends: true},
		{name: "between with a quitter", phase: between, quit: true, finishing: merge, want: between, pends: true},
		{name: "listening", phase: listening, quit: true, finishing: merge, want: winding, taken: true},
		{name: "winding, no quitter", phase: winding, finishing: merge, want: winding, pends: true},
		{name: "winding, something finishing", phase: winding, quit: true, finishing: merge, want: warned,
			pends: true},
		{name: "winding, nothing finishing", phase: winding, quit: true, want: quitting, quits: true},
		{name: "warned", phase: warned, quit: true, finishing: merge, want: quitting, quits: true},
		{name: "quitting", phase: quitting, quit: true, finishing: merge, want: quitting, quits: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := quitRigFor(t, &windingLoop{finishing: tc.finishing})
			if tc.quit {
				r.stops.quitWith(r.quitter())
			}
			var taken os.Signal
			r.stops.phase = tc.phase
			if tc.phase == listening {
				r.stops.onStop = func(s os.Signal) { taken = s }
			}
			r.stops.deliver(syscall.SIGTERM)
			if r.stops.phase != tc.want {
				t.Errorf("phase %v, want %v", r.stops.phase, tc.want)
			}
			if got := taken == syscall.SIGTERM; got != tc.taken {
				t.Errorf("onStop took the signal: %v, want %v", got, tc.taken)
			}
			if got := r.stops.pending == syscall.SIGTERM; got != tc.pends {
				t.Errorf("the signal pends: %v, want %v", got, tc.pends)
			}
			if got := len(r.exits) > 0; got != tc.quits {
				t.Errorf("orchestra quit: %v (exits %v), want %v", got, r.exits, tc.quits)
			}
		})
	}
}

// Only the first of the signals that pend is kept for the next phase.
func TestStopPhaseKeepsTheFirstPending(t *testing.T) {
	t.Parallel()
	r := quitRigFor(t, &windingLoop{})
	r.stops.deliver(syscall.SIGHUP)
	r.stops.deliver(os.Interrupt)
	if r.stops.pending != syscall.SIGHUP {
		t.Errorf("pending %v, want SIGHUP", r.stops.pending)
	}
}

// on starts the next phase: listening with a function, between without one, and winding at once
// when the function takes a signal that pends; windDown goes to winding, or stays warned.
func TestStopPhaseOnAndWindDown(t *testing.T) {
	t.Parallel()
	stops := catchStops()
	defer stops.release()
	stops.on(func(os.Signal) {})
	if stops.phase != listening {
		t.Errorf("on(f): phase %v, want listening", stops.phase)
	}
	stops.on(nil)
	if stops.phase != between {
		t.Errorf("on(nil): phase %v, want between", stops.phase)
	}
	stops.deliver(os.Interrupt)
	var took os.Signal
	stops.on(func(s os.Signal) { took = s })
	if stops.phase != winding || took != os.Interrupt || stops.pending != nil {
		t.Errorf("on(f) with a signal pending: phase %v, took %v, pending %v; want winding, Ctrl+C, none",
			stops.phase, took, stops.pending)
	}
	stops.on(nil)
	stops.windDown()
	if stops.phase != winding {
		t.Errorf("windDown: phase %v, want winding", stops.phase)
	}
	stops.phase = warned
	stops.windDown()
	if stops.phase != warned {
		t.Errorf("windDown when warned: phase %v, want warned", stops.phase)
	}
}
