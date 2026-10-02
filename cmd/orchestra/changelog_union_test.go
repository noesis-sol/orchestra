package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitAddsChangelogUnionOnlyWhenTold(t *testing.T) {
	repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "CHANGELOG.md"), []byte("# Changelog\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	attrs := filepath.Join(repo, ".gitattributes")

	stdout, stderr, err := runIn(t, repo, nil, "init", "--check", "make check", "-c", "1", "--check-timeout", "5m")
	if err != nil || !strings.Contains(stdout, "not asked (no terminal)") {
		t.Fatalf("init without a terminal: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if _, err := os.Stat(attrs); err == nil {
		t.Error("init without a terminal or --changelog-union wrote .gitattributes")
	}

	stdout, stderr, err = runIn(t, repo, nil, "init", "--changelog-union")
	if err != nil || !strings.Contains(stdout, "added CHANGELOG.md merge=union") || !strings.Contains(stdout, "Commit .orchestra/ and .gitattributes.") {
		t.Fatalf("init --changelog-union: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	if got := read(t, attrs); got != "CHANGELOG.md merge=union\n" {
		t.Errorf(".gitattributes = %q", got)
	}
}
