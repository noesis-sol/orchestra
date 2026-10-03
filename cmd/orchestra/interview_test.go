package main

import (
	"os"
	"syscall"
	"testing"
)

// While the interview has the terminal, Ctrl+C is Claude Code's: SIGINT neither stops the phase
// listening nor waits for the next one, while SIGTERM and SIGHUP still stop it. Afterwards Ctrl+C
// stops a phase again.
func TestInterruptsAreTheSessionsWhileItRuns(t *testing.T) {
	stops := catchStops()
	defer stops.release()
	var got []os.Signal
	stops.on(func(s os.Signal) { got = append(got, s) })
	restore := stops.dropInterrupts()
	stops.deliver(os.Interrupt)
	if len(got) != 0 {
		t.Fatalf("Ctrl+C during the session stopped the phase: %v", got)
	}
	stops.deliver(syscall.SIGTERM)
	if len(got) != 1 || got[0] != syscall.SIGTERM {
		t.Errorf("the phase got %v, want SIGTERM", got)
	}
	restore()
	stops.on(nil)

	restore = stops.dropInterrupts()
	stops.deliver(os.Interrupt) // between phases
	restore()
	if stops.pending != nil {
		t.Errorf("%v from the session waits for the next phase", stops.pending)
	}
	stops.on(func(s os.Signal) { got = append(got, s) })
	stops.deliver(os.Interrupt)
	if len(got) != 2 || got[1] != os.Interrupt {
		t.Errorf("after the session, the phase got %v, want Ctrl+C", got)
	}
	stops.on(nil)
}
