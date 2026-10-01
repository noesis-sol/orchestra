package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

func TestInitCorrectsConcurrencyTrimsCheckAndKeepsUnknownKeys(t *testing.T) {
	repo, _ := gitRepo(t)
	os.MkdirAll(filepath.Join(repo, project.Dir), 0o755)
	os.WriteFile(project.SettingsPath(repo), []byte(`{"concurrent": 20, "notes": "ours"}`), 0o644)
	stdout, stderr, err := runIn(t, repo, nil, "init", "--check", "  make check \n")
	if err != nil || !strings.Contains(stdout, "Sets this project up for orchestra") {
		t.Fatalf("init: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(read(t, project.SettingsPath(repo))), &m); err != nil {
		t.Fatal(err)
	}
	if m["concurrent"] != float64(1) || m["check"] != "make check" || m["notes"] != "ours" {
		t.Errorf("settings.json = %v", m)
	}
	s, _, _ := project.LoadSettings(repo)
	if _, err := project.ResolveConcurrency(0, s); err != nil {
		t.Errorf("a run after init: %v", err)
	}
}

func TestInitSavesTheCheckTimeout(t *testing.T) {
	repo, _ := gitRepo(t)
	stdout, stderr, err := runIn(t, repo, nil, "init", "--check", "make check", "--check-timeout", "90s", "-c", "1")
	if err != nil || !strings.Contains(stdout, "check: make check, stopped after 1m30s") {
		t.Fatalf("init: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	s, _, _ := project.LoadSettings(repo)
	if d, err := project.ResolveCheckTimeout(0, false, s); s.CheckTimeout != "1m30s" || err != nil || d != 90*time.Second {
		t.Errorf("settings = %+v, a run gets %s, %v", s, d, err)
	}
	_, stderr, err = runIn(t, repo, nil, "init", "--check-timeout", "0")
	if exitOf(err) != dispatch.ExitSetup || !strings.Contains(stderr, "--check-timeout must be a positive duration") {
		t.Errorf("--check-timeout 0: exit = %d, stderr:\n%s", exitOf(err), stderr)
	}
}
