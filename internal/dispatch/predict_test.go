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

// fakePredictor writes a stand-in for claude that records what it is asked, then runs script.
func fakePredictor(t *testing.T, script string) (bin, record string) {
	t.Helper()
	dir := t.TempDir()
	bin, record = filepath.Join(dir, "claude"), filepath.Join(dir, "asked")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ncat >> "+record+"\n"+script+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, record
}

// A ready ticket naming nothing gets its files predicted in the background and cached on it; a
// later pick keeps it apart from the running ticket that names one of them. The predictor is a
// command, which holds the bubble's clock while it runs.
func TestPredictedFootprintKeepsATicketApart(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.commitFiles("internal/x.go", "internal/y.go", "internal/z.go")
		h.cfg.Concurrency = 2
		h.cfg.Predict = true
		h.beads.add("A", "first", 1)
		h.beads.describe("A", "Change internal/x.go.")
		h.beads.add("Z", "second", 2)
		h.beads.describe("Z", "Change internal/z.go.")
		h.beads.add("B", "third", 3) // names nothing
		h.beads.add("C", "fourth", 4)
		h.beads.describe("C", "Change internal/y.go.")
		var v overlap
		h.worker("A", v.runs("A", func(w *fakeWorker) AgentState {
			time.Sleep(3 * readyPoll) // C takes Z's slot; then a few more polls, with a slot free
			return finishes("a.txt")(w)
		}))
		h.worker("Z", v.runs("Z", func(w *fakeWorker) AgentState {
			time.Sleep(time.Second) // B's files are predicted meanwhile
			if h.beads.metadata("B", PredictedKey) == "" {
				t.Error("B's files were not cached while Z ran")
			}
			return finishes("z.txt")(w)
		}))
		h.worker("B", v.runs("B", finishes("b.txt")))
		h.worker("C", v.runs("C", finishes("c.txt")))
		bin, asked := fakePredictor(t, `echo '{"structured_output":{"files":["internal/x.go","internal/nowhere.go"]}}'`)
		o := h.loop()
		o.organ = organ.Client{Bin: bin}
		if code := o.Run(t.Context()); code != ExitOK || o.Final() != "READY_EMPTY after 4 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.beads.metadata("B", PredictedKey); got != "internal/x.go" {
			t.Errorf("cached %q", got)
		}
		if got := v.of("B"); slices.Contains(got, "A") {
			t.Errorf("B ran beside A")
		}
		if got := h.sink.dispatched(); !equal(got, []string{"A", "Z", "C", "B"}) {
			t.Errorf("dispatched %v", got)
		}
		log := h.logged()
		for _, want := range []string{"B footprint predicted: internal/x.go\n", "skipping B: touches internal/x.go, like running A\n",
			"B footprint: internal/x.go (predicted)\n"} {
			if strings.Count(log, want) != 1 {
				t.Errorf("not logged once: %q\n%s", want, log)
			}
		}
		b, _ := os.ReadFile(asked)
		if n := strings.Count(string(b), "Predict the files ticket"); n != 1 || !strings.Contains(string(b), "Predict the files ticket B will change.\n") ||
			!strings.Contains(string(b), "internal/x.go\ninternal/y.go") {
			t.Errorf("the predictor was asked %d times, want once for B:\n%s", n, b)
		}
	})
}

// Dispatch never waits for a prediction: with the predictor hanging, tickets naming nothing run
// side by side, and the run ends without waiting for it. On the real clock: a command that hangs
// would hold a synctest bubble's clock until it ends.
func TestPredictionNeverDelaysDispatch(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.Concurrency = 2
	h.cfg.Predict = true
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A", func(w *fakeWorker) AgentState {
		eventually(t, "B never ran beside A", func() bool { return h.sink.dispatchedYet("B") })
		return finishes("a.txt")(w)
	})
	h.worker("B", finishes("b.txt"))
	bin, asked := fakePredictor(t, "exec sleep 60")
	o := h.loop()
	o.organ = organ.Client{Bin: bin}
	start := time.Now()
	if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if d := time.Since(start); d > 50*time.Second { // a loaded machine is slow, but not this slow
		t.Errorf("the run took %s: it waited for the predictor", d)
	}
	eventually(t, "the predictor was never asked", func() bool { _, err := os.Stat(asked); return err == nil })
	if got := h.beads.metadata("A", PredictedKey) + h.beads.metadata("B", PredictedKey); got != "" {
		t.Errorf("cached %q", got)
	}
	if log := h.logged(); strings.Contains(log, "predicted") || strings.Contains(log, "skipping") {
		t.Errorf("log:\n%s", log)
	}
}

// Without Predict (claude missing) or with footprints off, nothing is predicted.
func TestPredictorOffWithoutOrgansOrFootprints(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ predict, noFootprint bool }{{false, false}, {true, true}} {
		h := newHarness(t)
		h.cfg.Concurrency = 2
		h.cfg.Predict, h.cfg.NoFootprint = c.predict, c.noFootprint
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", finishes("a.txt"))
		h.worker("B", finishes("b.txt"))
		bin, asked := fakePredictor(t, `echo '{"structured_output":{"files":["a.txt"]}}'`)
		o := h.loop()
		o.organ = organ.Client{Bin: bin}
		if code := o.Run(context.Background()); code != ExitOK {
			t.Fatalf("%+v: exit %d\n%s", c, code, h.sink.text())
		}
		if _, err := os.Stat(asked); err == nil {
			t.Errorf("%+v: the predictor was asked", c)
		}
	}
}
