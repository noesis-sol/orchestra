package organ

import (
	"context"
	"os"
	"strings"
	"testing"
)

// The scout is told to leave out init's runners. A suite that runs one is returned as the scout gave it,
// with the others in their order: init drops it (TestOfferedSuites in internal/tui), as it would
// write it into the runner it runs.
func TestScoutIsToldToLeaveOutTheRunners(t *testing.T) {
	if !strings.Contains(scout.system, "Never list scripts/check-fast.sh or scripts/check-full.sh") {
		t.Error("the scout's prompt doesn't leave out the runners")
	}
	out := `{"is_error":false,"structured_output":{"suites":[` +
		`{"name":"check-fast","kind":"other","command":"scripts/check-fast.sh","found_in":"scripts/check-fast.sh:1",` +
		`"tier":"fast","parallel_safe":true,"needs":[]},` +
		`{"name":"check-full","kind":"other","command":"bash $PWD/scripts/check-full.sh","found_in":"Makefile:9",` +
		`"tier":"full","parallel_safe":true,"needs":[]},` +
		`{"name":"vet","kind":"lint","command":"go vet ./...","found_in":"scripts/check-fast.sh:8",` +
		`"tier":"fast","parallel_safe":true,"needs":[]}],"note":""}}`
	bin, record := fakeClaude(t, out)
	s, err := Client{Bin: bin}.Scout(context.Background(), scoutRepo(t, true))
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Suites) != 3 || s.Suites[0].Command != "scripts/check-fast.sh" || s.Suites[2].Command != "go vet ./..." {
		t.Errorf("got %+v, want the scout's three suites as it gave them", s.Suites)
	}
	if b, _ := os.ReadFile(record); !strings.Contains(string(b), "scripts/check-full.sh") {
		t.Errorf("claude's system prompt doesn't name the runners:\n%s", b)
	}
}
