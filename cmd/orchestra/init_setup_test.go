package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// Where the project has a lockfile, init offers the setup command: without a terminal or --setup it
// writes none and says how; --setup sets it, init keeps it after, and --setup "" removes it.
func TestInitOffersTheSetupCommandForALockfile(t *testing.T) {
	repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "package-lock.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	setup := func() string {
		s, _, err := project.LoadSettings(repo)
		if err != nil {
			t.Fatal(err)
		}
		return s.Setup
	}
	plain := func(s string) string { return strings.Join(strings.Fields(s), " ") }

	stdout, stderr, err := runIn(t, repo, nil, "init", "--check", "make check")
	if err != nil || setup() != "" || !strings.Contains(plain(stdout),
		"not asked (no terminal): found package-lock.json") || !strings.Contains(plain(stdout), "--setup 'npm ci'") {
		t.Fatalf("unasked: %v, setup %q\nstdout:\n%s\nstderr:\n%s", err, setup(), stdout, stderr)
	}
	stdout, stderr, err = runIn(t, repo, nil, "init", "--setup", " npm ci ")
	if err != nil || setup() != "npm ci" || !strings.Contains(plain(stdout), "'npm ci' runs before check-fast") {
		t.Fatalf("--setup: %v, setup %q\nstdout:\n%s\nstderr:\n%s", err, setup(), stdout, stderr)
	}
	stdout, _, err = runIn(t, repo, nil, "init")
	if err != nil || setup() != "npm ci" || !strings.Contains(plain(stdout), "settings.json has it") {
		t.Fatalf("kept: %v, setup %q\nstdout:\n%s", err, setup(), stdout)
	}
	stdout, _, err = runIn(t, repo, nil, "init", "--setup", "")
	if err != nil || setup() != "" || !strings.Contains(plain(stdout), "removed 'npm ci'") {
		t.Fatalf("removed: %v, setup %q\nstdout:\n%s", err, setup(), stdout)
	}
	if raw := read(t, project.SettingsPath(repo)); strings.Contains(raw, `"setup"`) {
		t.Errorf("settings.json still names a setup:\n%s", raw)
	}
}
