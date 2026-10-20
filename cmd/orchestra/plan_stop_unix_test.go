//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// Ctrl+C while plan --apply adds the first of several links lets that one finish and adds no other:
// plan --apply says which it added and which not, exit code 130.
func TestPlanApplyStoppedBetweenLinks(t *testing.T) {
	// Not parallel: it signals the test's own process, which the runs of tests beside it would catch too.
	if testing.Short() {
		t.Skip("skipped by -short: waits half a second for the signal to reach orchestra")
	}
	dir := t.TempDir()
	calls := filepath.Join(dir, "calls")
	list := `[{"id":"k-1","title":"Log rebases in Loop.merge","status":"open","priority":0},` +
		`{"id":"k-2","title":"Retry in Loop.merge","status":"open","priority":1},` +
		`{"id":"k-3","title":"Time Loop.merge","status":"open","priority":2},` +
		`{"id":"k-4","title":"Count Loop.merge","status":"open","priority":3}]`
	// The pause after SIGINT lets it reach orchestra before the next link.
	faketool.Write(t, dir, "bd", "#!/bin/sh\ncase \"$1\" in\n"+
		"list) echo '"+list+"' ;;\n"+
		"dep) [ -e '"+calls+"' ] || { kill -INT "+strconv.Itoa(os.Getpid())+"; sleep 0.5; }\n"+
		"\techo \"$@\" >> '"+calls+"' ;;\n"+
		"*) exit 1 ;;\nesac\n")
	onPath(t, dir)
	repo, _ := gitRepo(t)

	stdout, stderr, err := runIn(t, repo, nil, "plan", "--apply")
	if exitOf(err) != 130 {
		t.Errorf("exit %d, want 130\nstdout:\n%s\nstderr:\n%s", exitOf(err), stdout, stderr)
	}
	if got := read(t, calls); got != "dep add k-2 k-1\n" {
		t.Errorf("bd calls %q, want only dep add k-2 k-1", got)
	}
	if want := "orchestra plan: stopped. Added 1 of 3 links: k-2 waits for k-1. " +
		"Not added: k-3 waits for k-2, k-4 waits for k-3.\n"; stderr != want {
		t.Errorf("stderr:\n%s\nwant:\n%s", stderr, want)
	}
	if strings.Contains(stdout, "k-3 (P2) waits") || strings.Contains(stdout, "Added") {
		t.Errorf("stdout goes on past the stop:\n%s", stdout)
	}
}
