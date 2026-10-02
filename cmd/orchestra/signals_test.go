package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/tui"
)

// Each stop signal goes to one phase: the one listening, or the next to listen when it comes
// between phases, or while a stopped loop winds down before the watch knows how to quit (quitWith).
func TestEachStopSignalGoesToOnePhase(t *testing.T) {
	stops := catchStops()
	defer stops.release()
	var loop []os.Signal
	stops.on(func(s os.Signal) { loop = append(loop, s) })
	stops.deliver(os.Interrupt)
	stops.deliver(os.Interrupt) // a second Ctrl+C while the loop winds down
	stops.on(nil)
	if len(loop) != 1 || loop[0] != os.Interrupt {
		t.Errorf("the loop got %v, want the first Ctrl+C only", loop)
	}
	var organs []os.Signal
	stops.on(func(s os.Signal) { organs = append(organs, s) })
	if len(organs) != 1 || organs[0] != os.Interrupt {
		t.Errorf("the organ phase got %v, want the second Ctrl+C at once", organs)
	}
	stops.on(nil)
	if stops.pending != nil {
		t.Errorf("%v is left for a phase after the organs", stops.pending)
	}
	if stops.leaving() {
		t.Error("Ctrl+C doesn't ask orchestra to leave")
	}
	stops.deliver(syscall.SIGHUP)
	if !stops.leaving() {
		t.Error("SIGHUP asks orchestra to leave")
	}
}

// organRun runs the organ phase with the reviewer on and returns what it printed.
func organRun(t *testing.T, stops *stopWatch) string {
	t.Helper()
	log, err := dispatch.OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	organPhase(fakeOrgans{}, options{Review: true}, stops, log, dispatch.ExitInterrupted, "", tui.Printer{Out: &b},
		func() {})
	return b.String()
}

// A stop signal that came while the loop wound down skips the organ phase, as one during it does;
// SIGTERM or SIGHUP at any time in the run skips it too.
func TestAStopSignalBeforeTheOrganPhaseSkipsIt(t *testing.T) {
	for _, sigs := range [][]os.Signal{{os.Interrupt, os.Interrupt}, {os.Interrupt, syscall.SIGHUP}, {syscall.SIGTERM}} {
		stops := catchStops()
		stops.on(func(os.Signal) {}) // the loop's
		for _, s := range sigs {
			stops.deliver(s)
		}
		stops.on(nil)
		if out := organRun(t, stops); out != "" {
			t.Errorf("after %v the organ phase printed\n%s", sigs, out)
		}
		stops.release()
	}

	stops := catchStops()
	defer stops.release()
	stops.on(func(os.Signal) {})
	stops.deliver(os.Interrupt) // stops the loop only
	stops.on(nil)
	if out := organRun(t, stops); !strings.Contains(out, "ALL MERGED") {
		t.Errorf("after the Ctrl+C that stopped the loop the organ phase printed\n%s", out)
	}
}

// The feature run's context, like the organ phase's, is cancelled by the next stop signal, and
// stops listening once released.
func TestStopContextIsCancelledByTheNextSignal(t *testing.T) {
	stops := catchStops()
	defer stops.release()
	ctx, stop := stops.context(context.Background())
	stops.deliver(os.Interrupt)
	if ctx.Err() == nil {
		t.Error("the signal didn't cancel the context")
	}
	stop()

	_, stop = stops.context(context.Background())
	stop()
	stops.deliver(os.Interrupt)
	var next os.Signal
	stops.on(func(s os.Signal) { next = s })
	if next != os.Interrupt {
		t.Errorf("a signal after the context was released went to %v, want the next phase", next)
	}
}
