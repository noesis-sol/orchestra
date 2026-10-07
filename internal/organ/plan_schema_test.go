package organ

import (
	"encoding/json"
	"testing"
)

// The epic is two top-level strings: an "epic" object first in the schema made claude's first
// StructuredOutput call often malformed, and claude retried it (planSchema's comment).
func TestPlanSchemaGivesTheEpicFlat(t *testing.T) {
	var s struct {
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(planSchema), &s); err != nil {
		t.Fatal(err)
	}
	for name, p := range s.Properties {
		if p.Type == "object" {
			t.Errorf("planSchema's %s is an object", name)
		}
	}
	p, err := parsePlan(Result{Structured: json.RawMessage(goodPlan)}, planTracked)
	if err != nil {
		t.Fatal(err)
	}
	if p.Epic != (PlannedEpic{Title: "JSON output", Description: "Add --json to list."}) {
		t.Errorf("epic %+v", p.Epic)
	}
}
