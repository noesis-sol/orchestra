package organ

import (
	"encoding/json"
	"slices"
	"testing"
)

// TestSchemaEnums holds each enum in an organ's schema to the list its parser checks the answer
// against: a value added to one side only would pass the schema and fail parsing, or the reverse.
func TestSchemaEnums(t *testing.T) {
	for _, c := range []struct {
		organ, schema string
		path          []string // from the schema's root to the property with the enum
		want          []string
	}{
		{"triage", triageSchema, []string{"properties", "cause"}, triageCauses},
		{"triage", triageSchema, []string{"properties", "confidence"}, triageConfidences},
		{"scout", scoutSchema, []string{"properties", "suites", "items", "properties", "kind"}, asStrings(suiteKinds)},
		{"scout", scoutSchema, []string{"properties", "suites", "items", "properties", "tier"}, asStrings(tiers)},
	} {
		var node map[string]any
		if err := json.Unmarshal([]byte(c.schema), &node); err != nil {
			t.Fatalf("%s's schema isn't JSON: %v", c.organ, err)
		}
		for _, key := range c.path {
			next, ok := node[key].(map[string]any)
			if !ok {
				t.Fatalf("%s's schema has no object at %v", c.organ, c.path)
			}
			node = next
		}
		raw, ok := node["enum"].([]any)
		if !ok {
			t.Fatalf("%s's schema has no enum at %v", c.organ, c.path)
		}
		var got []string
		for _, v := range raw {
			s, ok := v.(string)
			if !ok {
				t.Fatalf("%s's schema has a value that isn't a string in the enum at %v: %v", c.organ, c.path, v)
			}
			got = append(got, s)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s's schema allows %q at %v, its parser %q", c.organ, got, c.path, c.want)
		}
	}
}

// asStrings is a list of string-typed values as plain strings.
func asStrings[S ~string](values []S) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}
