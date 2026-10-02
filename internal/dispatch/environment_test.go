package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// givesUp is a worker whose environment fails it: it settles at once, without claiming its ticket
// or changing anything.
func givesUp(w *fakeWorker) AgentState { return "idle" }

func (h *harness) holdForEnvironment(window time.Duration) {
	h.cfg.EnvHoldCount, h.cfg.EnvHoldWindow = 2, window
}

func (h *harness) dispatched() []string {
	var ids []string
	for _, ev := range h.sink.of(EvDispatch) {
		ids = append(ids, strings.Fields(ev)[0])
	}
	return ids
}

func (h *harness) statusOf(id string) string {
	st, _ := h.beads.Status(context.Background(), id)
	return st
}

func TestWorkersFailingAtOnceHoldTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.beads.add("C", "c", 3) // no behaviour: a worker on it fails the test
		h.worker("A", givesUp)
		h.worker("B", givesUp)

		o, code := h.run()
		if code != ExitEnvironment {
			t.Errorf("exit code %d, want %d:\n%s", code, ExitEnvironment, h.logged())
		}
		if got := h.dispatched(); !equal(got, []string{"A", "B"}) {
			t.Errorf("dispatched %v, want A and B only", got)
		}
		final := "ENVIRONMENT: the last 2 tickets (A, B) each settled within 1m of starting without being claimed or changed; check the machine, then restart"
		if o.Final() != final || !h.alerts.has("Stopped: ENVIRONMENT") {
			t.Errorf("final line %q, want %q, notified as stopped: %q", o.Final(), final, h.alerts.list())
		}
		// The tickets did nothing: back in the queue, not set aside, their notes kept.
		for _, id := range []string{"A", "B", "C"} {
			if st := h.statusOf(id); st != "open" {
				t.Errorf("%s is %s, want open", id, st)
			}
		}
		for _, id := range []string{"A", "B"} {
			if n := h.beads.notesOf(id); !strings.Contains(n, "settled with the ticket still 'open'") || !strings.Contains(n, "reopened") {
				t.Errorf("%s's notes:\n%s", id, n)
			}
		}
		if aside := o.setAside(); len(aside) != 0 {
			t.Errorf("set aside: %v", aside)
		}
	})
}

func TestOneWorkerFailingAtOnceChangesNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.beads.add("C", "c", 3)
		h.worker("A", givesUp)
		h.worker("B", finishes("b.txt")) // ends the row
		h.worker("C", givesUp)

		_, code := h.run()
		if code != ExitOK {
			t.Errorf("exit code %d, want %d:\n%s", code, ExitOK, h.logged())
		}
		for id, want := range map[string]string{"A": "deferred", "B": "closed", "C": "deferred"} {
			if st := h.statusOf(id); st != want {
				t.Errorf("%s is %s, want %s", id, st, want)
			}
		}
		if strings.Contains(h.logged(), "ENVIRONMENT") {
			t.Errorf("held for the environment:\n%s", h.logged())
		}
	})
}

func TestWorkersFailingLaterDontHoldTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		givesUpLater := func(w *fakeWorker) AgentState { time.Sleep(2 * time.Minute); return "idle" } // after the window
		h.worker("A", givesUpLater)
		h.worker("B", givesUpLater)

		if _, code := h.run(); code != ExitOK {
			t.Errorf("exit code %d, want %d:\n%s", code, ExitOK, h.logged())
		}
		for _, id := range []string{"A", "B"} {
			if st := h.statusOf(id); st != "deferred" {
				t.Errorf("%s is %s, want deferred", id, st)
			}
		}
	})
}

// fakeTriage is a claude that blames the environment for every deferral, with high confidence.
func fakeTriage(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "claude")
	script := `#!/bin/sh
cat >/dev/null
echo '{"structured_output":{"cause":"environment","confidence":"high","summary":"Classifier unavailable.","recommendation":"Retry later."}}'
`
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestTriageBlamingTheEnvironmentHoldsTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		defers := func(w *fakeWorker) AgentState { w.claim(); w.deferIt(); return "idle" }
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.beads.add("C", "c", 3)
		h.beads.add("D", "d", 4) // no behaviour: a worker on it fails the test
		h.worker("A", defers)
		h.worker("B", defers)
		// Triage runs beside the loop, so C may start before the second verdict: then it finishes once
		// the run holds.
		h.worker("C", func(w *fakeWorker) AgentState {
			<-h.sink.held
			return finishes("c.txt")(w)
		})

		o := h.loop()
		o.organ = organ.Client{Bin: fakeTriage(t)}
		o.StartTriage()
		code := o.Run(t.Context())
		o.FinishTriage(t.Context())
		if code != ExitEnvironment {
			t.Errorf("exit code %d, want %d:\n%s", code, ExitEnvironment, h.logged())
		}
		final := "ENVIRONMENT: triage blamed the environment for the last 2 tickets (A, B) with high confidence (Classifier unavailable); check the machine, then restart"
		if o.Final() != final {
			t.Errorf("final line %q, want %q", o.Final(), final)
		}
		if got := h.dispatched(); len(got) > 3 {
			t.Errorf("dispatched %v, want no fourth ticket", got)
		}
		// Their workers claimed and deferred them: they did something, so they stay set aside.
		for _, id := range []string{"A", "B"} {
			if st := h.statusOf(id); st != "deferred" {
				t.Errorf("%s is %s, want deferred", id, st)
			}
		}
	})
}

// runsProbe is a probe worker on a machine that works again: it runs the command it was given.
func runsProbe(w *fakeWorker) AgentState {
	if err := os.WriteFile(filepath.Join(w.wt, ".orchestra", "run", "probe"), []byte("ok\n"), 0o644); err != nil {
		w.t.Error(err)
	}
	return "idle"
}

// probeTab is the tab the probe worker was started in, or "".
func (h *harness) probeTab() string {
	h.herdr.mu.Lock()
	defer h.herdr.mu.Unlock()
	for _, p := range h.herdr.panes {
		if p.ticket == probeID {
			return p.tab
		}
	}
	return ""
}

func TestAProbeThatRunsItsCommandEndsTheHold(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		h.cfg.EnvProbe = time.Minute
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.beads.add("C", "c", 3)
		h.worker("A", givesUp, finishes("a.txt"))
		h.worker("B", givesUp, finishes("b.txt"))
		h.worker("C", finishes("c.txt"))
		h.worker(probeID, runsProbe)

		o, code := h.run()
		if code != ExitOK {
			t.Errorf("exit code %d, want %d:\n%s", code, ExitOK, h.logged())
		}
		if got := h.dispatched(); !equal(got, []string{"A", "B", "A", "B", "C"}) {
			t.Errorf("dispatched %v, want A and B, then after the probe A, B and C", got)
		}
		for _, id := range []string{"A", "B", "C"} {
			if st := h.statusOf(id); st != "closed" {
				t.Errorf("%s is %s, want closed", id, st)
			}
		}
		probing := "PROBE: the run holds for the environment; in 1m one worker without a ticket runs a command, and if it does the run takes tickets again"
		ok := "PROBE_OK: a worker without a ticket ran a command 1m after the hold; taking tickets again"
		if !strings.Contains(h.logged(), probing) || !strings.Contains(h.logged(), ok) {
			t.Errorf("no PROBE or PROBE_OK line:\n%s", h.logged())
		}
		if !strings.HasPrefix(o.Final(), "READY_EMPTY") {
			t.Errorf("final line %q", o.Final())
		}
		if tab := h.probeTab(); tab == "" || !slices.Contains(h.herdr.tabsClosed(), tab) {
			t.Errorf("probe tab %q not closed: %v", tab, h.herdr.tabsClosed())
		}
		if exists(filepath.Join(h.repo, ".orchestra", "run", "probe")) {
			t.Error("the probe's file is left behind")
		}
	})
}

// The probe runs one echo, so a Claude probe worker starts without MCP servers.
func TestTheProbeStartsWithoutMCPServers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		h.cfg.EnvProbe = time.Minute
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.worker("A", givesUp, finishes("a.txt"))
		h.worker("B", givesUp, finishes("b.txt"))
		h.worker(probeID, runsProbe)

		if _, code := h.run(); code != ExitOK {
			t.Fatalf("exit code %d, want %d:\n%s", code, ExitOK, h.logged())
		}
		if got := h.herdr.argsFor(probeID); len(got) != 1 || !equal(got[0], []string{"start", "--strict-mcp-config"}) {
			t.Errorf("probe started with %q, want once with --strict-mcp-config", got)
		}
	})
}

// A Herdr that refuses arguments starts the probe worker plainly.
func TestTheProbeStartsPlainlyWhenHerdrRefusesArguments(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		h.cfg.EnvProbe = time.Minute
		h.herdr.refuseArgs = true
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.worker("A", givesUp, finishes("a.txt"))
		h.worker("B", givesUp, finishes("b.txt"))
		h.worker(probeID, runsProbe)

		if _, code := h.run(); code != ExitOK {
			t.Fatalf("exit code %d, want %d:\n%s", code, ExitOK, h.logged())
		}
		got := h.herdr.argsFor(probeID)
		if len(got) != 2 || !equal(got[0], []string{"start", "--strict-mcp-config"}) || !equal(got[1], []string{"start"}) {
			t.Errorf("probe started with %q, want --strict-mcp-config, then no arguments", got)
		}
		if !strings.Contains(h.logged(), errRefused.Error()) {
			t.Errorf("log lacks the refusal:\n%s", h.logged())
		}
	})
}

func TestAProbeThatRunsNoCommandEndsTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		h.cfg.EnvProbe = time.Minute
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.worker("A", givesUp)
		h.worker("B", givesUp)
		h.worker(probeID, givesUp)

		o, code := h.run()
		if code != ExitEnvironment {
			t.Errorf("exit code %d, want %d:\n%s", code, ExitEnvironment, h.logged())
		}
		final := "ENVIRONMENT: the last 2 tickets (A, B) each settled within 1m of starting without being claimed or changed; " +
			"a worker probing the machine 1m later failed too: it stopped without running its command; see tab " + h.probeTab() +
			"; check the machine, then restart"
		if o.Final() != final {
			t.Errorf("final line %q, want %q", o.Final(), final)
		}
		if closed := h.herdr.tabsClosed(); slices.Contains(closed, h.probeTab()) {
			t.Errorf("the failed probe's tab was closed: %v", closed)
		}
		for _, id := range []string{"A", "B"} {
			if st := h.statusOf(id); st != "open" {
				t.Errorf("%s is %s, want open", id, st)
			}
		}
	})
}

func TestTheMachineIsProbedOncePerRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.holdForEnvironment(time.Minute)
		h.cfg.EnvProbe = time.Minute
		h.beads.add("A", "a", 1)
		h.beads.add("B", "b", 2)
		h.worker("A", givesUp, givesUp)
		h.worker("B", givesUp, givesUp)
		h.worker(probeID, runsProbe) // a second probe would find no behaviour and fail the test

		o, code := h.run()
		if code != ExitEnvironment {
			t.Errorf("exit code %d, want %d:\n%s", code, ExitEnvironment, h.logged())
		}
		if got := h.dispatched(); !equal(got, []string{"A", "B", "A", "B"}) {
			t.Errorf("dispatched %v, want A and B twice", got)
		}
		final := "ENVIRONMENT: the last 2 tickets (A, B) each settled within 1m of starting without being claimed or changed; check the machine, then restart"
		if o.Final() != final {
			t.Errorf("final line %q, want %q", o.Final(), final)
		}
	})
}
