package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// With neither --machine-workers nor its variable, the workers across the machine's runs are capped at
// half its cores, at least 2.
func TestDefaultMachineWorkers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ cores, want int }{{1, 2}, {2, 2}, {3, 2}, {4, 2}, {10, 5}, {18, 9}} {
		if got := DefaultMachineWorkers(tc.cores); got != tc.want {
			t.Errorf("DefaultMachineWorkers(%d) = %d, want %d", tc.cores, got, tc.want)
		}
	}
	if got, want := DefaultMachineWorkersFrom(10), "default: half of 10 cores"; got != want {
		t.Errorf("DefaultMachineWorkersFrom(10) = %q, want %q", got, want)
	}
}

// The START line says the cap on the workers across the machine's runs and what gave it, and, once,
// that the run's own concurrent is above it.
func TestTheStartLineSaysTheMachineWorkers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		concurrency int
		cap         int
		from        string
		want        string
		above       bool
	}{
		{"default", 3, 5, DefaultMachineWorkersFrom(10), ", machine workers: 5 (default: half of 10 cores), ", false},
		{"flag", 2, 6, "--machine-workers", ", machine workers: 6 (--machine-workers), ", false},
		{"no cap", 8, 0, "ORCHESTRA_MACHINE_WORKERS", ", machine workers: none (ORCHESTRA_MACHINE_WORKERS), ", false},
		{"unsaid", 2, 0, "", ", machine workers: none, ", false},
		{"above the cap", 6, 5, DefaultMachineWorkersFrom(10), ", machine workers: 5 (default: half of 10 cores), ", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.cfg.Concurrency, h.cfg.MachineWorkers, h.cfg.MachineWorkersFrom = tc.concurrency, tc.cap, tc.from
				h.run() // nothing ready: the run starts, and ends there
				logged := h.logged()
				start := strings.Index(logged, "START orchestra ")
				if start < 0 {
					t.Fatalf("no START line:\n%s", logged)
				}
				line, _, _ := strings.Cut(logged[start:], "\n")
				if !strings.Contains(line, tc.want) {
					t.Errorf("the START line doesn't say %q:\n%s", tc.want, line)
				}
				above := "is above the machine's cap of"
				if n := strings.Count(logged, above); n != map[bool]int{true: 1, false: 0}[tc.above] {
					t.Errorf("the run says %d times that its concurrent is above the cap, want it said %v:\n%s",
						n, tc.above, logged)
				}
			})
		})
	}
}
