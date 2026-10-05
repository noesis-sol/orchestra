package main

import (
	"os"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// The setup and the files whose change has it run come from settings.json.
func TestConfigSetup(t *testing.T) {
	configFixture(t, `{"concurrent": 1}`)
	if c, p := loadWith(t); len(p) > 0 || c.Setup != "" ||
		strings.Join(c.SetupFiles, " ") != strings.Join(project.DefaultSetupFiles, " ") {
		t.Errorf("default: %q %v %v", c.Setup, c.SetupFiles, p)
	}
	settings := `{"setup": "npm ci", "setup_files": ["package-lock.json"]}`
	if err := os.WriteFile(".orchestra/settings.json", []byte(settings), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, p := loadWith(t); len(p) > 0 || c.Setup != "npm ci" || strings.Join(c.SetupFiles, " ") != "package-lock.json" {
		t.Errorf("settings: %q %v %v", c.Setup, c.SetupFiles, p)
	}
	if err := os.WriteFile(".orchestra/settings.json", []byte(`{"setup_files": ["["]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "setup_files must be a list") {
		t.Errorf("invalid setting: %v", p)
	}
}
