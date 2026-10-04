package beads

import "testing"

// FuzzParseLinked gives parseLinked any output of 'bd list --json'. It doesn't panic, and what it
// reads has blocks links naming both tickets, and tickets with no dependencies of their own. go test
// runs the seeds; docs/development.md says how to fuzz.
func FuzzParseLinked(f *testing.F) {
	for _, raw := range []string{
		`{"schema_version":1,"data":[
		  {"id":"a","status":"open","priority":2,"issue_type":"task","created_at":"2026-09-30T14:33:10Z",
		   "dependencies":[{"issue_id":"a","depends_on_id":"b","type":"blocks"},{"issue_id":"a","depends_on_id":"c","type":"related"}]},
		  {"id":"b","status":"in_progress","dependencies":[{"issue_id":"b","depends_on_id":"z","type":"blocks"}]},
		  {"id":"e","status":"open","issue_type":"epic"},
		  {"id":"q","status":"open","labels":["human"]}
		]}`,
		`[{"id":"a","dependencies":[{"issue_id":"","depends_on_id":"b","type":"blocks"}]},{"id":"b","dependencies":null}]`,
		`{"data":null}`, `[null]`, `[]`, `null`, "not json", "",
	} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		tickets, links, err := parseLinked(raw)
		if err != nil {
			return
		}
		for _, l := range links {
			if l.Blocker == "" || l.Blocked == "" {
				t.Errorf("%s: a link without both tickets: %+v", raw, l)
			}
		}
		for _, tk := range tickets {
			if tk.Dependencies != nil {
				t.Errorf("%s: %s keeps its dependencies: %+v", raw, tk.ID, tk.Dependencies)
			}
		}
	})
}
