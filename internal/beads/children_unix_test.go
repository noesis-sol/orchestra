//go:build unix

package beads

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Children lists a ticket's subtickets with their metadata, and the blocks links among their
// dependencies, leaving the other kinds of link out.
func TestChildrenListsTheSubticketsAndTheirLinks(t *testing.T) {
	record := createBd(t, `{"schema_version":1,"data":[`+
		`{"id":"k-1.1","title":"Encoder","status":"open","issue_type":"feature","priority":1,`+
		`"metadata":{"files":["enc.go"]},"dependencies":[{"issue_id":"k-1.1","depends_on_id":"k-1","type":"parent-child"}]},`+
		`{"id":"k-1.2","title":"Flag","status":"open","issue_type":"task","priority":2,`+
		`"dependencies":[{"issue_id":"k-1.2","depends_on_id":"k-1.1","type":"blocks"},`+
		`{"issue_id":"k-1.2","depends_on_id":"k-9","type":"related"}]}]}`)
	children, links, err := Tracker{Repo: t.TempDir()}.Children(context.Background(), "k-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(children) != 2 || children[0].ID != "k-1.1" || string(children[0].Metadata) != `{"files":["enc.go"]}` ||
		children[1].IssueType != "task" || children[1].Dependencies != nil {
		t.Errorf("children %+v", children)
	}
	if want := []dispatch.Link{{Blocker: "k-1.1", Blocked: "k-1.2"}}; !reflect.DeepEqual(links, want) {
		t.Errorf("links %v, want %v", links, want)
	}
	if b, _ := os.ReadFile(record); string(b) != "list\n--json\n--limit\n0\n--parent\nk-1\n" {
		t.Errorf("bd ran with\n%s", b)
	}

	createBd(t, `Error: database is locked`)
	if _, _, err := (Tracker{Repo: t.TempDir()}).Children(context.Background(), "k-1"); err == nil {
		t.Error("output that isn't JSON should be an error")
	}
}
