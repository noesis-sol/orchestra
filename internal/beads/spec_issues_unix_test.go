//go:build unix

package beads

import (
	"context"
	"os"
	"reflect"
	"testing"
)

// WithMetadata lists every ticket, closed ones too, whose metadata gives the key the value, with its
// text, metadata and parent.
func TestWithMetadataListsTheTicketsCarryingIt(t *testing.T) {
	record := createBd(t, `[{"id":"s-1","title":"An app","status":"open","issue_type":"epic",`+
		`"metadata":{"spec":"app.md"}},{"id":"s-1.1","title":"List","status":"closed","issue_type":"task",`+
		`"parent":"s-1","acceptance_criteria":"lists","metadata":{"spec":"app.md","files":["l.go"]}}]`)
	got, err := Tracker{Repo: t.TempDir()}.WithMetadata(context.Background(), "spec", "app.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Parent != "s-1" || got[1].AcceptanceCriteria != "lists" || got[1].Status != "closed" {
		t.Errorf("got %+v", got)
	}
	if b, _ := os.ReadFile(record); string(b) != "list\n--json\n--limit\n0\n--all\n--metadata-field\nspec=app.md\n" {
		t.Errorf("bd ran with\n%s", b)
	}
}

// Cycles reads bd dep cycles, as an object listing each cycle's members or, from an older bd, as the
// list of its tickets; none is no cycle, and output that isn't JSON is an error.
func TestCyclesReadsBothShapes(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want [][]string
	}{
		{`[]`, nil},
		{`{"schema_version":1,"data":[]}`, nil},
		{`[{"members":[{"id":"s-2","issue":{"id":"s-2"}},{"id":"s-3"}],"partial":false}]`, [][]string{{"s-2", "s-3"}}},
		{`[[{"id":"s-4","title":"A"},{"id":"s-5","title":"B"}]]`, [][]string{{"s-4", "s-5"}}},
	} {
		record := createBd(t, tc.out)
		got, err := Tracker{Repo: t.TempDir()}.Cycles(context.Background())
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, %v; want %v", tc.out, got, err, tc.want)
		}
		if b, _ := os.ReadFile(record); string(b) != "dep\ncycles\n--json\n" {
			t.Errorf("bd ran with\n%s", b)
		}
	}
	createBd(t, `Error: no database`)
	if _, err := (Tracker{Repo: t.TempDir()}).Cycles(context.Background()); err == nil {
		t.Error("output that isn't JSON should be an error")
	}
}
