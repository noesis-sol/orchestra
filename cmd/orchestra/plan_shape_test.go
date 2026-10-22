//go:build unix

package main

import (
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// orchestra plan says, for each epic with open tickets, how their blocks links let them run, before its
// proposal, which stays as it was: here no links to add, exit 0.
func TestPlanPrintsEachEpicsShape(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dep := func(id, on string) string {
		return `{"issue_id":"` + id + `","depends_on_id":"` + on + `","type":"blocks"}`
	}
	ticket := func(id, parent string, deps ...string) string {
		return `{"id":"` + id + `","title":"Part ` + id + `","status":"open","priority":2,"issue_type":"task",` +
			`"parent":"` + parent + `","dependencies":[` + strings.Join(deps, ",") + `]}`
	}
	list := "[" + strings.Join([]string{
		ticket("p-aq.1", "p-aq"),
		ticket("p-aq.2", "p-aq", dep("p-aq.2", "p-aq.1")),
		ticket("p-aq.3", "p-aq", dep("p-aq.3", "p-aq.1")),
		ticket("p-aq.4", "p-aq", dep("p-aq.4", "p-aq.1")),
		ticket("p-ch.1", "p-ch"),
		ticket("p-ch.2", "p-ch", dep("p-ch.2", "p-ch.1")),
		ticket("p-loose", ""),
	}, ",") + "]"
	faketool.Write(t, dir, "bd", "#!/bin/sh\ncase \"$1\" in\nlist) echo '"+list+"' ;;\n*) exit 1 ;;\nesac\n")
	onPath(t, dir)
	repo, _ := gitRepo(t)

	stdout, stderr, err := runIn(t, repo, nil, "plan")
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, stderr)
	}
	want := "The epics' open tickets, as their blocks links let them run:\n" +
		"  p-aq: 4 tickets, longest chain 2 (aq.1 > aq.2), at most 2 at a time; p-aq.1 holds 3\n" +
		"  p-ch: 2 tickets, longest chain 2 (ch.1 > ch.2), at most 1 at a time\n" +
		"No links to add: no two of the 7 open tickets touch the same code without being ordered already.\n"
	if stdout != want {
		t.Errorf("plan output\n%s\nwant\n%s", stdout, want)
	}
}
