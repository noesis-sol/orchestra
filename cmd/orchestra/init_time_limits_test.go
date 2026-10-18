package main

import (
	"os"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// init asks no time limit: a first init writes the defaults to settings.json, to edit there, and a
// second keeps the ones edited by hand.
func TestInitWritesTheDefaultTimeLimitsAndKeepsEditedOnes(t *testing.T) {
	t.Parallel()
	repo := initRepo(t)
	if _, stderr, err := runIn(t, repo, nil, "init", "-c", "1", "--check-fast", "make check"); err != nil {
		t.Fatalf("init: %v\n%s", err, stderr)
	}
	m := settingsOf(t, repo)
	if m["check_fast_timeout"] != project.DefaultCheckTimeoutText ||
		m["check_full_timeout"] != project.DefaultCheckFullTimeoutText {
		t.Errorf("a first init: settings.json = %v; want the default time limits", m)
	}

	path := project.SettingsPath(repo)
	edited := strings.NewReplacer(`"check_fast_timeout": "30m"`, `"check_fast_timeout": "7m"`,
		`"check_full_timeout": "60m"`, `"check_full_timeout": "2h"`).Replace(read(t, path))
	if err := os.WriteFile(path, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := runIn(t, repo, nil, "init"); err != nil {
		t.Fatalf("a second init: %v\n%s", err, stderr)
	}
	if m = settingsOf(t, repo); m["check_fast_timeout"] != "7m" || m["check_full_timeout"] != "2h" {
		t.Errorf("a second init: settings.json = %v; want the time limits edited by hand", m)
	}
}
