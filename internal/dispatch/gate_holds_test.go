package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// The usage hold an organ starts while Run waits, on nothing but the gate, lifts at its known reset:
// the gate wakes Run for its end, with no worker's result or merge to wake it.
func TestTheUsageHoldLiftsAtItsEnd(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		o := New(Config{Concurrency: 1, Limit: NoLimit, UsageLimitWait: 6 * time.Hour}, &Log{}, "", Deps{})
		sink := &runSink{held: make(chan struct{})}
		o.SetSink(sink)
		r := newRunState(context.Background(), o)
		defer r.machine.unregister()

		start := time.Now()
		reset := start.Add(time.Hour).Truncate(time.Minute)
		go func() {
			time.Sleep(time.Minute) // Run waits already
			o.usage.refused("triage of Q", "You've hit your session limit · resets "+reset.Local().Format("3:04pm"))
		}()
		for range 3 { // the organ's hold wakes Run for its new end, then its end does
			if why, _ := r.wait(waits{}); why != wokeGate {
				t.Fatalf("woken: %v, want the gate", why)
			}
			if !o.usage.holds() {
				break
			}
		}
		if o.usage.holds() || !r.taking() {
			t.Fatalf("the hold holds still: holds %v, taking %v\n%s", o.usage.holds(), r.taking(), sink.text())
		}
		if lifted := sink.eventsOf(EvLimitLift); len(lifted) != 1 || !lifted[0].Time.Equal(reset.Add(resetGrace)) {
			t.Errorf("lifted: %+v, want one at %v\n%s", lifted, reset.Add(resetGrace), sink.text())
		}
	})
}

// The hold for the machine's cap on workers reads the other runs at its end, every machinePoll, and
// lifts at the first end after another run's worker finishes, with none of this run's own to wake Run.
func TestTheMachineHoldLiftsAtItsEnd(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "runs")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		other := filepath.Join(dir, "run-424242-1.json")
		writeOther := func(running int) {
			t.Helper()
			body := fmt.Sprintf(`{"pid":424242,"repo":"/elsewhere","concurrent":2,"started":"1999-12-31T00:00:00Z",`+
				`"running":%d}`, running)
			if err := os.WriteFile(other, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		writeOther(1)
		o := New(Config{Concurrency: 2, Limit: NoLimit, MachineWorkers: 2, Machine: dir}, &Log{}, "", Deps{})
		o.process = func(int) (time.Time, bool) { return time.Time{}, true }
		sink := &runSink{held: make(chan struct{})}
		o.SetSink(sink)
		r := newRunState(context.Background(), o)
		defer r.machine.unregister()
		r.inflight["A"] = true // this run's one worker, which goes on throughout

		r.lookAround()
		if !r.machineFull() || !o.gate.held(gateMachine) || !r.taking() {
			t.Fatalf("at the cap: full %v, held %v, taking %v", r.machine.full, o.gate.held(gateMachine), r.taking())
		}
		start := time.Now()
		for range 3 {
			if why, _ := r.wait(waits{}); why != wokeEnd {
				t.Fatalf("still at the cap: %v, want the hold's end", why)
			}
		}
		if waited := time.Since(start); waited != 3*machinePoll {
			t.Errorf("woken three times in %v, want %v", waited, 3*machinePoll)
		}
		time.Sleep(machinePoll / 2)
		writeOther(0)
		freed := time.Now()
		if why, _ := r.wait(waits{}); why != wokeGate || o.gate.held(gateMachine) || r.machine.full {
			t.Fatalf("a slot freed: %v, held %v, full %v; want the gate", why, o.gate.held(gateMachine), r.machine.full)
		}
		if waited := time.Since(freed); waited != machinePoll/2 {
			t.Errorf("lifted %v after the slot freed, want at the hold's next end, %v later", waited, machinePoll/2)
		}
	})
}

// The hold for the environment, taken up on the gate, ends at Config.EnvProbe with nothing running:
// the probe starts then, and once it has run its command the hold lifts.
func TestTheEnvironmentHoldLiftsAtItsEnd(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		h.cfg.EnvProbe = 10 * time.Minute
		h.worker(probeID, runsProbe)
		o := h.loop()
		r := newRunState(context.Background(), o)
		defer r.machine.unregister()

		o.holdForEnvironment(context.Background(), "the machine fails")
		r.heedEnvironment()
		if !o.gate.held(gateEnv) || r.taking() {
			t.Fatalf("held for the environment: held %v, taking %v", o.gate.held(gateEnv), r.taking())
		}
		start := time.Now()
		if !r.idle() {
			t.Fatalf("the run does not go on after the probe:\n%s", h.sink.text())
		}
		if o.gate.held(gateEnv) || !r.taking() {
			t.Errorf("after the probe: held %v, taking %v", o.gate.held(gateEnv), r.taking())
		}
		var probed time.Time
		for _, ev := range h.sink.events {
			if strings.Contains(ev.Text, "probe worker") && strings.Contains(ev.Text, "started") {
				probed = ev.Time
			}
		}
		if want := start.Add(h.cfg.EnvProbe); !probed.Equal(want) {
			t.Errorf("the probe started at %v, want at the hold's end, %v\n%s", probed, want, h.sink.text())
		}
	})
}
