package organ

import (
	"context"
	"os"
	"strings"
	"testing"
)

// Each organ runs at its own effort, low for triage and the predictor and medium for the report,
// unless the client sets one for them all.
func TestOrgansRunAtTheirEffort(t *testing.T) {
	const output = `{"type":"result","is_error":false,"result":"report",` +
		`"structured_output":{"cause":"problem","confidence":"low","summary":"s","recommendation":"r","files":[]}}`
	ctx := context.Background()
	organs := []struct {
		name string
		call func(g Client) error
		own  string
	}{
		{"triage", func(g Client) error { _, err := g.Triage(ctx, Deferral{ID: "k-1"}); return err }, "low"},
		{"predictor", func(g Client) error { _, err := g.PredictFiles(ctx, Footprint{ID: "k-1"}); return err }, "low"},
		{"reviewer", func(g Client) error { _, err := g.Review(ctx, "evidence"); return err }, "medium"},
	}
	for _, o := range organs {
		for _, set := range []string{"", "high"} {
			bin, record := fakeClaude(t, output)
			if err := o.call(Client{Bin: bin, Effort: set}); err != nil {
				t.Fatalf("%s: %v", o.name, err)
			}
			want := o.own
			if set != "" {
				want = set
			}
			b, _ := os.ReadFile(record)
			if got := string(b); !strings.Contains(got, "[--effort]\n["+want+"]\n") || strings.Count(got, "[--effort]") != 1 {
				t.Errorf("%s with Effort %q: want --effort %s once:\n%s", o.name, set, want, got)
			}
		}
	}
}
