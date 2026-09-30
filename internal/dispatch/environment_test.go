package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// givesUp is a worker whose environment fails it: it settles at once, without claiming its ticket
// or changing anything.
func givesUp(w *fakeWorker) string { return "idle" }

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
	st, _ := h.beads.Status(id)
	return st
}

func TestWorkersFailingAtOnceHoldTheRun(t *testing.T) {
	h := newHarness(t)
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
	if o.Final() != final || !h.alerts.has(final) {
		t.Errorf("final line %q, want %q, notified", o.Final(), final)
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
}

func TestOneWorkerFailingAtOnceChangesNothing(t *testing.T) {
	h := newHarness(t)
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
}

func TestWorkersFailingLaterDontHoldTheRun(t *testing.T) {
	h := newHarness(t)
	h.holdForEnvironment(time.Nanosecond) // every worker settles after the window
	h.beads.add("A", "a", 1)
	h.beads.add("B", "b", 2)
	h.worker("A", givesUp)
	h.worker("B", givesUp)

	if _, code := h.run(); code != ExitOK {
		t.Errorf("exit code %d, want %d:\n%s", code, ExitOK, h.logged())
	}
	for _, id := range []string{"A", "B"} {
		if st := h.statusOf(id); st != "deferred" {
			t.Errorf("%s is %s, want deferred", id, st)
		}
	}
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
	h := newHarness(t)
	h.holdForEnvironment(time.Minute)
	defers := func(w *fakeWorker) string { w.claim(); w.deferIt(); return "idle" }
	h.beads.add("A", "a", 1)
	h.beads.add("B", "b", 2)
	h.beads.add("C", "c", 3)
	h.beads.add("D", "d", 4) // no behaviour: a worker on it fails the test
	h.worker("A", defers)
	h.worker("B", defers)
	// Triage runs beside the loop, so C may start before the second verdict: then it finishes once
	// the run holds.
	h.worker("C", func(w *fakeWorker) string {
		select {
		case <-h.sink.held:
		case <-time.After(5 * time.Second):
			t.Error("no HOLD while C ran")
		}
		return finishes("c.txt")(w)
	})

	o := h.loop()
	o.organ = organ.Client{Bin: fakeTriage(t)}
	o.StartTriage()
	code := o.Run(context.Background())
	o.FinishTriage(context.Background())
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
}
