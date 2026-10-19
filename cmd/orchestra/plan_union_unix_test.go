//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// orchestra plan asks git how it merges a file: two tickets naming a short changelog it merges by
// union (merge=union in .gitattributes) aren't linked, and are once the attribute is gone.
func TestPlanLeavesOutUnionMergedFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	list := `[{"id":"k-1","title":"Add a CHANGELOG.md entry for the retry","status":"open","priority":1},` +
		`{"id":"k-2","title":"Add a CHANGELOG.md entry for the dashboard","status":"open","priority":2}]`
	faketool.Write(t, dir, "bd", "#!/bin/sh\ncase \"$1\" in\nlist) echo '"+list+"' ;;\n*) exit 1 ;;\nesac\n")
	onPath(t, dir)
	repo, git := gitRepo(t)
	attrs := filepath.Join(repo, ".gitattributes")
	for name, content := range map[string]string{"CHANGELOG.md": "# Changelog\n", ".gitattributes": "CHANGELOG.md merge=union\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(repo, "add", ".")
	git(repo, "commit", "-q", "-m", "changelog")

	stdout, stderr, err := runIn(t, repo, nil, "plan")
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "No links to add") {
		t.Errorf("a changelog merged by union linked tickets:\n%s", stdout)
	}

	if err := os.WriteFile(attrs, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err = runIn(t, repo, nil, "plan")
	if err != nil {
		t.Fatalf("plan: %v\n%s", err, stderr)
	}
	if want := "k-2 (P2) waits for k-1 (P1): both touch CHANGELOG.md"; !strings.Contains(stdout, want) {
		t.Errorf("without merge=union, plan output lacks %q:\n%s", want, stdout)
	}
}
