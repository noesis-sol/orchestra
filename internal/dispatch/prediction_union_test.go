package dispatch

import (
	"context"
	"slices"
	"testing"
)

// unionCheckout is a clean checkout whose .gitattributes merges the changelog by union.
type unionCheckout struct{ cleanCheckout }

func (unionCheckout) Attribute(ctx context.Context, repo, attr, path string) (string, error) {
	if attr == "merge" && path == "CHANGELOG.md" {
		return "union", nil
	}
	return "unspecified", nil
}

// A prediction handed to a running ticket leaves out the files git merges by union, as the
// footprints of ready tickets do: two workers' lines in the changelog can't conflict.
func TestGivenPredictionLeavesOutFilesMergedByUnion(t *testing.T) {
	t.Parallel()
	o := &Loop{predictor: newPredictor(), ledger: newLedger()}
	o.checkout = unionCheckout{}
	o.footprints["A"] = &runFootprint{}
	tracked := []string{"CHANGELOG.md", "internal/a.go"}
	o.givePrediction(t.Context(), Ticket{ID: "A", Title: "Say hello"}, []string{"CHANGELOG.md", "internal/a.go"}, tracked)
	if fp := o.footprints["A"].fp; !slices.Equal(fp.Files, []string{"internal/a.go"}) || !fp.Predicted {
		t.Errorf("given the running ticket: %+v, want only internal/a.go, predicted", fp)
	}
	o.footprints["B"] = &runFootprint{}
	o.givePrediction(t.Context(), Ticket{ID: "B", Title: "Say hello"}, []string{"CHANGELOG.md"}, tracked)
	if fp := o.footprints["B"].fp; !fp.Empty() {
		t.Errorf("given only the changelog: %+v, want empty", fp)
	}
}
