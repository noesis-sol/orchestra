package organ

import (
	"context"
	"os"
	"strings"
	"testing"
)

// The scout is told to leave out init's runners, and a suite that runs one is dropped all the same:
// init would write it into the runner it runs.
func TestScoutLeavesOutTheRunners(t *testing.T) {
	if !strings.Contains(scoutSystem, "Never list scripts/check-fast.sh or scripts/check-full.sh") {
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
	if len(s.Suites) != 2 || s.Suites[0].Command != checkScript || s.Suites[1].Command != "go vet ./..." {
		t.Errorf("got %+v, want scripts/check.sh, then vet", s.Suites)
	}
	if b, _ := os.ReadFile(record); !strings.Contains(string(b), "scripts/check-full.sh") {
		t.Errorf("claude's system prompt doesn't name the runners:\n%s", b)
	}
}

func TestRunsRunner(t *testing.T) {
	for command, want := range map[string]bool{
		"scripts/check-fast.sh": true, "./scripts/check-full.sh": true, "bash scripts/check-fast.sh": true,
		"$PWD/scripts/check-fast.sh": true, "/home/me/repo/scripts/check-full.sh": true,
		"make lint && scripts/check-fast.sh": true, "sh -c 'scripts/check-full.sh'": true,
		"scripts/check.sh": false, "scripts/check-fast.sh.bak": false, "npm test": false,
	} {
		if got := runsRunner(command); got != want {
			t.Errorf("runsRunner(%q) = %v, want %v", command, got, want)
		}
	}
}
