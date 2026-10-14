package project

import (
	"os"
	"path/filepath"
	"testing"
)

// init doesn't save over settings it can't read: they would save as nothing but init's choices.
func TestApplySettingsKeepsSettingsItCannotRead(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".orchestra"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{"concurrent": "4", "worker_effort": "high", "terminal": "tmux", "setup": "npm ci"}` + "\n")
	if err := os.WriteFile(SettingsPath(repo), before, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplySettings(repo, Choice{Concurrent: 2}); err == nil {
		t.Error("ApplySettings saved settings it could not read")
	}
	after, err := os.ReadFile(SettingsPath(repo))
	if err != nil || string(after) != string(before) {
		t.Errorf("settings.json is now %q, %v; want it as it was, %q", after, err, before)
	}
}
