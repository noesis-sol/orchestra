package dispatch

import (
	"slices"
	"testing"
)

// A ticket's notes say what happened to it, not where it works: the files and functions they name
// are no part of its footprint, so it overlaps no ticket over them.
func TestTicketFootprintLeavesOutNotes(t *testing.T) {
	t.Parallel()
	reopened := Ticket{ID: "A", Title: "Cover the SDK's contract", Description: "Add a test of the SDK's contract.",
		Notes: "Reopened: internal/dispatch/run.go still fails in Loop.merge; see internal/tui/run.go and `pickNext`."}
	fp := TicketFootprint(reopened, trackedHere, "")
	if !fp.Empty() {
		t.Errorf("footprint from the notes: %+v", fp)
	}
	for _, other := range []Ticket{
		{ID: "B", Description: "Change internal/dispatch/run.go."},
		{ID: "C", Description: "Rename Loop.merge and pickNext()."},
		{ID: "D", Metadata: []byte(`{"files": "internal/tui/run.go"}`)},
	} {
		if what := shared(fp, TicketFootprint(other, trackedHere, ""), nil, nil); what != "" {
			t.Errorf("A overlaps %s over %q, named only in A's notes", other.ID, what)
		}
	}

	// The same words in its description are where it works.
	reopened.Description += " " + reopened.Notes
	fp = TicketFootprint(reopened, trackedHere, "")
	if !slices.Contains(fp.Files, "internal/dispatch/run.go") || !slices.Contains(fp.Funcs, "Loop.merge") {
		t.Errorf("footprint from the description: %+v", fp)
	}
}
