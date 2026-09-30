package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

func TestInitCorrectsConcurrencyTrimsCheckAndKeepsUnknownKeys(t *testing.T) {
	repo, _ := gitRepo(t)
	os.MkdirAll(filepath.Join(repo, project.Dir), 0o755)
	os.WriteFile(project.SettingsPath(repo), []byte(`{"concurrent": 20, "notes": "ours"}`), 0o644)
	var code int
	out := stdoutOf(t, func() { code = runInit(repo, []string{"--check", "  make check \n"}) })
	if code != dispatch.ExitOK {
		t.Fatalf("exit = %d:\n%s", code, out)
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
