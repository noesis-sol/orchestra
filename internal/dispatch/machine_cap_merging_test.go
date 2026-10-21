package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// orchestra-qw5v.3: the cap on the workers across the machine's runs counts the workers at work, those
// taking the runs' slots, and not their tickets waiting to merge, which free their slots: a run alone
// with concurrent at the cap starts the next ticket while one merges, as it does without the cap, and
// tells the other runs only of its workers at work.
func TestALoneRunAtTheCapStartsTheNextTicketWhileOneMerges(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := sharing(t, h)
		h.cfg.Concurrency, h.cfg.MachineWorkers = 2, 2
		h.beads.add("A", "merges slowly", 1)
		h.beads.add("B", "at work all along", 2)
		h.beads.add("C", "takes A's slot while A merges", 3)
		h.worker("A", finishes("a.txt"))
		h.worker("B", busyFor(3*time.Hour, "b.txt"))
		h.worker("C", busyFor(2*time.Hour, "c.txt"))
		o := h.loop()
		o.tabs = slowTabClose{fakeHerdr: h.herdr, ticket: "A", takes: time.Hour}
		done := make(chan int, 1)
		go func() { done <- o.Run(t.Context()) }()

		time.Sleep(30 * time.Minute) // A merging, its tab closing; B and C at work
		if ev := h.sink.text(); !strings.Contains(ev, "C dispatching") || strings.Contains(ev, "A closed") {
			t.Errorf("C should have started while A merges:\n%s", ev)
		}
		if got := runningRegistered(t, dir); got != 2 {
			t.Errorf("the run's registration says %d workers running while A merges, want 2 (B and C)", got)
		}

		if code := <-done; code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if ev := h.sink.text(); strings.Contains(ev, "HOLD: MACHINE") {
			t.Errorf("a lone run with concurrent at the cap held for the machine:\n%s", ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A", "C", "B"}) {
			t.Errorf("closed %v, want A, C and B", got)
		}
	})
}

// runningRegistered is the count of workers running in the one run registered in dir.
func runningRegistered(t *testing.T, dir string) int {
	t.Helper()
	names := registered(t, dir)
	if len(names) != 1 {
		t.Fatalf("registered %v, want the run's file alone", names)
	}
	b, err := os.ReadFile(filepath.Join(dir, names[0]))
	if err != nil {
		t.Fatal(err)
	}
	var reg registration
	if err := json.Unmarshal(b, &reg); err != nil {
		t.Fatal(err)
	}
	return reg.Running
}
