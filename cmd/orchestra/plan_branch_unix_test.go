//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// orchestra plan footprints a ticket with commits on its branch (wt/<id>) from the files its branch
// changes on top of the checked-out branch: k-2, whose text names only pick.go, is ordered after
// k-1 over loop.go once its branch changes that, and not before.
func TestPlanGoesByABranchsFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	list := `[{"id":"k-1","title":"Split loop.go in two","status":"open","priority":1},` +
		`{"id":"k-2","title":"Speed up pick.go","status":"open","priority":2}]`
	faketool.Write(t, dir, "bd", "#!/bin/sh\ncase \"$1\" in\nlist) echo '"+list+"' ;;\n*) exit 1 ;;\nesac\n")
	onPath(t, dir)
	repo, git := gitRepo(t)
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("loop.go", "package x\n")
	write("pick.go", "package x\n")
	git(repo, "add", ".")
	git(repo, "commit", "-q", "-m", "code")

	stdout, stderr, err := runIn(t, repo, nil, "plan")
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "No links to add") {
		t.Errorf("tickets naming different files linked:\n%s", stdout)
	}

	git(repo, "branch", "wt/k-2")
	git(repo, "switch", "-q", "wt/k-2")
	write("loop.go", "package x\n\nfunc pick() {}\n")
	git(repo, "commit", "-q", "-am", "k-2: pick in the loop")
	git(repo, "switch", "-q", "-")
	stdout, stderr, err = runIn(t, repo, nil, "plan")
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, stderr)
	}
	if want := "k-2 (P2) waits for k-1 (P1): both touch loop.go"; !strings.Contains(stdout, want) {
		t.Errorf("k-2's branch changes loop.go, but plan output lacks %q:\n%s", want, stdout)
	}
	if strings.Contains(stderr, "branches") {
		t.Errorf("plan warned of the branches:\n%s", stderr)
	}
}
