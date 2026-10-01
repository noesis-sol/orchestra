package beads

import (
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

func TestParseOpenKeepsWorkAndItsBlocksLinks(t *testing.T) {
	raw := `{"schema_version":1,"data":[
	  {"id":"a","status":"open","priority":2,"issue_type":"task","created_at":"2026-09-30T14:33:10Z",
	   "dependencies":[{"issue_id":"a","depends_on_id":"b","type":"blocks"},{"issue_id":"a","depends_on_id":"c","type":"related"}]},
	  {"id":"b","status":"in_progress","dependencies":[{"issue_id":"b","depends_on_id":"z","type":"blocks"}]},
	  {"id":"e","status":"open","issue_type":"epic"},
	  {"id":"q","status":"open","labels":["human"]}
	]}`
	open, links, err := parseOpen([]byte(raw), []string{"epic"})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].ID != "a" || open[0].CreatedAt != "2026-09-30T14:33:10Z" || open[0].Dependencies != nil {
		t.Errorf("open = %+v, want only a, without its link entries", open)
	}
	if want := []dispatch.Link{{Blocker: "b", Blocked: "a"}, {Blocker: "z", Blocked: "b"}}; !slices.Equal(links, want) {
		t.Errorf("links = %+v, want %+v", links, want)
	}
	if _, _, err := parseOpen([]byte("not json"), nil); err == nil {
		t.Error("garbage should be an error")
	}
}
