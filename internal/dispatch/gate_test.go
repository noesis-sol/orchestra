package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// The gate is open until something holds it, and the merges read why the run takes on no more work
// from it as Run sets it: a stop first, then winding down; the ticket limit keeps only new tickets
// from starting, which the merges go on through.
func TestTheGateSaysWhyTheRunTakesNoTickets(t *testing.T) {
	t.Parallel()
	g := newGate()
	if !g.open() || g.why() != "" {
		t.Fatalf("a new gate: open %v, why %q", g.open(), g.why())
	}
	if !g.set(gateLimit, gateHold{on: true}) || g.set(gateLimit, gateHold{on: true}) {
		t.Error("set should report that the limit's hold changed once")
	}
	if g.open() || g.why() != "" {
		t.Errorf("at the limit: open %v, why %q; want closed, with nothing for the merges", g.open(), g.why())
	}
	g.set(gateDrain, gateHold{on: true, why: "the run is winding down"})
	if why := g.why(); why != "the run is winding down" {
		t.Errorf("winding down: why %q", why)
	}
	s := halt(ExitDirty, stopDirtyTree, ": dirty")
	if !g.addStop(s) || g.addStop(halt(ExitTool, stopGitFailed, ": git")) {
		t.Error("addStop should report the first stop only")
	}
	if why := g.why(); why != "the run holds (DIRTY_TREE: dirty)" {
		t.Errorf("stopped: why %q", why)
	}
	if g.firstStop() != s || g.onlyStop() != nil || g.stopLine() != "DIRTY_TREE: dirty; also GIT_FAILED: git" {
		t.Errorf("stops: first %v, only %v, line %q", g.firstStop(), g.onlyStop(), g.stopLine())
	}

	g = newGate()
	g.addStop(s)
	if g.onlyStop() != s {
		t.Errorf("one stop: only %v", g.onlyStop())
	}
	g.replaceStop(nil)
	if !g.open() || g.firstStop() != nil {
		t.Errorf("the stop lifted: open %v, first %v", g.open(), g.firstStop())
	}
}

// next gives the earliest end of the holds that hold, and none once they are lifted.
func TestTheGateGivesTheEarliestEnd(t *testing.T) {
	t.Parallel()
	g := newGate()
	if until, wake := g.next(); !until.IsZero() || wake == nil {
		t.Fatalf("a new gate: until %v, wake %v", until, wake)
	}
	soon, later := time.Now().Add(time.Minute), time.Now().Add(time.Hour)
	g.set(gateDrain, gateHold{on: true, until: later})
	g.set(gateLimit, gateHold{on: true, until: soon})
	if until, _ := g.next(); !until.Equal(soon) {
		t.Errorf("until %v, want %v", until, soon)
	}
	g.set(gateLimit, gateHold{})
	if until, _ := g.next(); !until.Equal(later) {
		t.Errorf("the earlier hold lifted: until %v, want %v", until, later)
	}
	g.set(gateDrain, gateHold{})
	if until, _ := g.next(); !until.IsZero() {
		t.Errorf("nothing holds: until %v", until)
	}
}

// Run's wait wakes for the gate, as a hold lifts elsewhere or comes to its end, hears a drain request
// on its way, and wakes for the loop's own channels.
func TestRunsWaitWakesForTheGate(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		o := New(Config{Concurrency: 1, Limit: NoLimit}, &Log{}, "", Deps{})
		sink := &runSink{held: make(chan struct{})}
		o.SetSink(sink)
		r := newRunState(context.Background(), o)
		defer r.machine.unregister()

		o.gate.wakeRun()
		o.gate.wakeRun() // one wake waiting is enough
		if why, _ := r.wait(waits{}); why != wokeGate {
			t.Errorf("woken: %v, want the gate", why)
		}

		start := time.Now()
		o.gate.set(gateDrain, gateHold{on: true, until: start.Add(time.Hour)})
		if why, _ := r.wait(waits{timer: time.After(2 * time.Hour)}); why != wokeGate {
			t.Errorf("at the hold's end: %v, want the gate", why)
		}
		if waited := time.Since(start); waited != time.Hour {
			t.Errorf("waited %v, want the hold's hour", waited)
		}
		// Come to its end, a hold lifts or moves its end: until then the gate wakes Run at once.
		if why, _ := r.wait(waits{}); why != wokeGate {
			t.Errorf("past the hold's end: %v, want the gate", why)
		}
		o.gate.set(gateDrain, gateHold{on: true, why: "the run is winding down"})

		o.setDrain(false, "")
		if why, _ := r.wait(waits{}); why != wokeDrain || r.drained() {
			t.Errorf("a request to take tickets again: %v, drained %v", why, r.drained())
		}
		if ev := sink.text(); !strings.Contains(ev, "DRAIN cancelled") {
			t.Errorf("events:\n%s", ev)
		}
		if why, _ := r.wait(waits{timer: time.After(time.Minute)}); why != wokeTimer {
			t.Errorf("nothing else: %v, want the timer", why)
		}
		results := make(chan result, 1)
		results <- result{id: "A"}
		if why, res := r.wait(waits{results: results}); why != wokeResult || res.id != "A" {
			t.Errorf("a result: %v, %+v", why, res)
		}
	})
}
