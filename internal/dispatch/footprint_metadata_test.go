package dispatch

import (
	"slices"
	"strings"
	"testing"
)

// The forms a files or predicted_files entry comes in, including a list stored as a string, which
// is what bd update <id> --set-metadata 'files=["a.go","b.go"]' writes.
func TestMetadataListReadsEveryForm(t *testing.T) {
	organ := []string{"internal/organ/screen.go", "internal/organ/screen_test.go", "internal/organ/organ.go"}
	two := []string{"internal/tui/run.go", "docs/new.md"}
	cases := []struct {
		name, meta string
		want       []string
	}{
		{"a list", `{"files": ["internal/tui/run.go", "docs/new.md"]}`, two},
		{"commas", `{"files": "internal/tui/run.go,docs/new.md"}`, two},
		{"commas and spaces", `{"files": "internal/tui/run.go, docs/new.md"}`, two},
		{"spaces and lines", `{"files": "internal/tui/run.go\n\tdocs/new.md "}`, two},
		{"the object in a string", `"{\"files\":\"internal/tui/run.go docs/new.md\"}"`, two},
		// orchestra-181.1's metadata, as bd show --json gave it.
		{"a list in a string",
			`{"files": "[\"internal/organ/screen.go\",\"internal/organ/screen_test.go\",\"internal/organ/organ.go\"]"}`, organ},
		{"a list in a string, spaced", `{"files": " [ \"internal/tui/run.go\", \"docs/new.md\" ] "}`, two},
		{"a list in a string in the object in a string",
			`"{\"files\":\"[\\\"internal/tui/run.go\\\",\\\"docs/new.md\\\"]\"}"`, two},
		{"an empty list in a string", `{"files": "[]"}`, []string{}},
		{"a list missing its bracket", `{"files": "[\"internal/tui/run.go\", \"docs/new.md\""}`, two},
		{"a list of numbers in a string", `{"files": "[\"internal/tui/run.go\", 3]"}`, []string{"internal/tui/run.go", "3"}},
		{"single quotes and backticks", "{\"files\": \"['internal/tui/run.go', `docs/new.md`]\"}", two},
		{"no entry", `{"team": "x"}`, nil},
		{"a number", `{"files": 3}`, nil},
		{"no object", `[]`, nil},
	}
	for _, c := range cases {
		for _, key := range []string{FilesKey, PredictedKey} {
			meta := strings.ReplaceAll(c.meta, `files`, key)
			got := metadataList([]byte(meta), key)
			if !slices.Equal(got, c.want) {
				t.Errorf("%s (%s): %q, want %q", c.name, key, got, c.want)
			}
		}
	}
}

// A list stored as a string leaves no quotes or brackets in the footprint, whether it is the
// ticket's files or its predicted files.
func TestTicketFootprintReadsAListInAString(t *testing.T) {
	tracked := append(slices.Clone(trackedHere),
		"internal/organ/screen.go", "internal/organ/screen_test.go", "internal/organ/organ.go")
	want := []string{"internal/organ/organ.go", "internal/organ/screen.go", "internal/organ/screen_test.go"}
	inString := `"[\"internal/organ/screen.go\",\"internal/organ/screen_test.go\",\"internal/organ/organ.go\"]"`

	fp := TicketFootprint(Ticket{Title: "Add the screen organ", Metadata: []byte(`{"files": ` + inString + `}`)}, tracked, "")
	if !slices.Equal(fp.Files, want) || fp.Predicted {
		t.Errorf("files: %+v", fp)
	}
	if got := fp.String(); strings.ContainsAny(got, `"[]`) {
		t.Errorf("String() = %q", got)
	}

	fp = TicketFootprint(Ticket{Title: "Say hello", Metadata: []byte(`{"predicted_files": ` + inString + `}`)}, tracked, "")
	if !slices.Equal(fp.Files, want) || !fp.Predicted {
		t.Errorf("predicted files: %+v", fp)
	}
}
