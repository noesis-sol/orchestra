package organ

import (
	"context"
	"os"
	"strings"
	"testing"
)

// The scout says, for each suite, how to run it on part of the project, for the worker prompt init
// writes: a command with a placeholder for the part, trimmed. One without a placeholder would run the
// whole suite, or nothing a worker can fill in, and is dropped, as it is for a suite without one.
func TestScoutSaysHowToRunASuiteOnPartOfTheProject(t *testing.T) {
	bin, record := fakeClaude(t, `{"is_error":false,"structured_output":{"suites":[`+
		`{"name":"unit","kind":"unit","command":"go test ./...","targeted":" go test ./<package>/... ",`+
		`"found_in":"Makefile:3","tier":"fast","parallel_safe":true,"needs":[]},`+
		`{"name":"pytest","kind":"unit","command":"make test","targeted":"pytest -q <paths>",`+
		`"found_in":"Makefile:5","tier":"fast","parallel_safe":true,"needs":[]},`+
		`{"name":"lint","kind":"lint","command":"npm run lint","targeted":"npm run lint",`+
		`"found_in":"package.json:6","tier":"fast","parallel_safe":true,"needs":[]},`+
		`{"name":"empty","kind":"other","command":"make x","targeted":"make x <>",`+
		`"found_in":"Makefile:9","tier":"fast","parallel_safe":true,"needs":[]},`+
		`{"name":"build","kind":"build","command":"make build","targeted":"",`+
		`"found_in":"Makefile:7","tier":"full","parallel_safe":true,"needs":[]}],"stack":[],"note":""}}`)
	s, err := Client{Bin: bin}.Scout(context.Background(), scoutRepo(t, false))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"go test ./<package>/...", "pytest -q <paths>", "", "", ""}
	if len(s.Suites) != len(want) {
		t.Fatalf("suites %+v", s.Suites)
	}
	for i, su := range s.Suites {
		if su.Targeted != want[i] {
			t.Errorf("suite %s: targeted %q, want %q", su.Name, su.Targeted, want[i])
		}
	}
	b, _ := os.ReadFile(record)
	for _, want := range []string{`"targeted":{"type":"string"}`, `"command","targeted","found_in"`,
		"- targeted: the command that runs the same suite on part of the project", `"pytest <paths>"`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the scout isn't asked %q:\n%s", want, b)
		}
	}
}
