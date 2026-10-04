//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// fakePlanBd puts a bd on PATH that lists two open tickets naming Loop.merge and records every
// dep add in the file it returns.
func fakePlanBd(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	list := `[{"id":"k-2","title":"Retry in Loop.merge","status":"open","priority":3},` +
		`{"id":"k-1","title":"Log rebases in Loop.merge","status":"open","priority":1},` +
		`{"id":"k-3","title":"Unrelated","status":"open","priority":0}]`
	script := "#!/bin/sh\ncase \"$1\" in\n" +
		"list) echo '" + list + "' ;;\n" +
		"dep) echo \"$@\" >> '" + calls + "' ;;\n" +
		"*) exit 1 ;;\nesac\n"
	faketool.Write(t, dir, "bd", script)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

func TestPlanProposesAndAppliesOnlyWithApply(t *testing.T) {
	calls := fakePlanBd(t)
	repo, _ := gitRepo(t)

	stdout, stderr, err := runIn(t, repo, nil, "plan")
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, stderr)
	}
	for _, want := range []string{"Proposed 1 blocks link between the 3 open tickets", "k-2 (P3) waits for k-1 (P1): both touch Loop.merge", "Nothing changed"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("plan output lacks %q:\n%s", want, stdout)
		}
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Errorf("plan without --apply ran bd dep: %v", err)
	}

	stdout, stderr, err = runIn(t, repo, nil, "plan", "--apply")
	if err != nil {
		t.Fatalf("plan --apply: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "Added 1 link.") {
		t.Errorf("plan --apply output:\n%s", stdout)
	}
	if got := read(t, calls); got != "dep add k-2 k-1\n" {
		t.Errorf("bd calls %q, want dep add k-2 k-1", got)
	}

	if _, _, err := runIn(t, repo, nil, "plan", "extra"); exitOf(err) != 2 {
		t.Errorf("plan extra: exit %d", exitOf(err))
	}
}
