package dispatch

import (
	"slices"
	"testing"
)

// A merge check that runs the full one with -short names only its own script: the files the full
// check names are left out of a ticket's footprint as well, and so are a merge check's alone.
func TestTicketFootprintLeavesOutTheFullChecksFiles(t *testing.T) {
	t.Parallel()
	tracked := append(slices.Clone(trackedWithCheck), "scripts/check-fast.sh")
	text := Ticket{Description: "Say hello in internal/tui/run.go.",
		AcceptanceCriteria: "scripts/check-fast.sh and scripts/check.sh pass."}
	for _, c := range []Config{
		{Check: "scripts/check-fast.sh", CheckFull: "scripts/check.sh"},
		{Check: "scripts/check-fast.sh scripts/check.sh"},
		{CheckFull: "scripts/check-fast.sh && scripts/check.sh"},
	} {
		if fp := TicketFootprint(text, tracked, checkCommands(c)); !slices.Equal(fp.Files, []string{"internal/tui/run.go"}) {
			t.Errorf("checks %q and %q: files %q, want only internal/tui/run.go", c.Check, c.CheckFull, fp.Files)
		}
	}
	c := Config{Check: "scripts/check-fast.sh"}
	if fp := TicketFootprint(text, tracked, checkCommands(c)); !slices.Equal(fp.Files,
		[]string{"internal/tui/run.go", "scripts/check.sh"}) {
		t.Errorf("no full check: files %q, want internal/tui/run.go and scripts/check.sh", fp.Files)
	}
}
