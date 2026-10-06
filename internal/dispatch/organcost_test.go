package dispatch

import (
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/organ"
)

// costlyOrgan is a claude that keeps the input of its last call in the file it returns and answers as
// triage and as the reviewer at once, each call costing $0.0125 over 2 turns.
func costlyOrgan(t *testing.T) (bin, input string) {
	t.Helper()
	dir := t.TempDir()
	input = filepath.Join(dir, "input")
	script := "#!/bin/sh\ncat > '" + input + "'\n" + `cat <<'JSON'
{"type":"result","subtype":"success","is_error":false,"result":"All merged.","stop_reason":"end_turn",
"structured_output":{"cause":"problem","confidence":"low","summary":"s","recommendation":"r"},
"total_cost_usd":0.0125,"num_turns":2,"session_id":"s-1"}
JSON
`
	return faketool.Write(t, dir, "claude", script), input
}

// Each organ call the loop makes is logged with its cost and turns; the reviewer is told what the
// organs cost before it, and the report ends with the run's organ cost, its own call included.
func TestTheRunsOrganCostIsLoggedAndReported(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		o := h.loop()
		bin, input := costlyOrgan(t)
		o.organ = organ.Client{Bin: bin}

		if _, err := o.organs().Triage(t.Context(), organ.Deferral{ID: "A"}); err != nil {
			t.Fatal(err)
		}
		report, err := o.Review(t.Context(), ExitOK, "DONE")
		if err != nil {
			t.Fatal(err)
		}

		logged := h.logged()
		for _, want := range []string{" ORGAN triage: $0.0125 in 2 turns, ", " ORGAN review: $0.0125 in 2 turns, "} {
			if !strings.Contains(logged, want) {
				t.Errorf("log lacks %q:\n%s", want, logged)
			}
		}
		if given := read(t, input); !strings.Contains(given,
			"\n$0.0125 in 1 organ call (triage 1), 2 turns in all, before this report.\n") {
			t.Errorf("the reviewer's evidence lacks the organ cost:\n%s", given)
		}
		if want := "\nOrgans: $0.0250 in 2 organ calls (review 1, triage 1), 4 turns in all.\n"; !strings.HasSuffix(report,
			want) {
			t.Errorf("report %q, want it to end with %q", report, want)
		}
	})
}

// Before any organ call, the reviewer is told none said what it cost, and the report says nothing of it.
func TestNoOrganCostBeforeAnyCall(t *testing.T) {
	o := &Loop{}
	if got := o.organCostEvidence(); got != "No organ call before this report said what it cost." {
		t.Errorf("evidence %q", got)
	}
	if got := o.organCostLine(); got != "" {
		t.Errorf("report line %q, want none", got)
	}
}
