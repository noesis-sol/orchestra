package main

import (
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

// The default cap is half the machine's cores, at least 2, and says so; a cap given, 0 included, wins.
func TestSettleMachineWorkers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		given project.Given[int]
		cores int
		want  int
		from  string
	}{
		{"2 cores", project.Given[int]{}, 2, 2, "default: half of 2 cores"},
		{"3 cores", project.Given[int]{}, 3, 2, "default: half of 3 cores"},
		{"10 cores", project.Given[int]{}, 10, 5, "default: half of 10 cores"},
		{"18 cores", project.Given[int]{}, 18, 9, "default: half of 18 cores"},
		{"given", project.Given[int]{Value: 7, Given: true, Name: "--machine-workers"}, 10, 7, "--machine-workers"},
		{"no cap", project.Given[int]{Given: true, Name: "ORCHESTRA_MACHINE_WORKERS"}, 10, 0, "ORCHESTRA_MACHINE_WORKERS"},
	} {
		var c dispatch.Config
		settleMachineWorkers(&c, tc.given, tc.cores)
		if c.MachineWorkers != tc.want || c.MachineWorkersFrom != tc.from {
			t.Errorf("%s: MachineWorkers = %d from %q, want %d from %q",
				tc.name, c.MachineWorkers, c.MachineWorkersFrom, tc.want, tc.from)
		}
	}
}

// The overnight pre-flight's offer of a worker count keeps to the default cap, as a run does.
func TestOfferedConcurrencyUnderTheDefaultCap(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ cores, want int }{{2, 2}, {3, 2}, {10, 2}, {12, 3}, {18, 4}} {
		if got := offeredConcurrency(tc.cores, dispatch.DefaultMachineWorkers(tc.cores)); got != tc.want {
			t.Errorf("offeredConcurrency on %d cores = %d, want %d", tc.cores, got, tc.want)
		}
	}
}
