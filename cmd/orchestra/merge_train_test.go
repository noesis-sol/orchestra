package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigMergeTrain(t *testing.T) {
	t.Parallel()
	configFixture(t, `{"concurrent": 2}`)
	if c, p := loadWith(t); len(p) > 0 || c.MergeTrain != 1 {
		t.Errorf("default: %d, %v", c.MergeTrain, p)
	}
	settings := filepath.Join(workDir(t), ".orchestra", "settings.json")
	if err := os.WriteFile(settings, []byte(`{"concurrent": 2, "merge_train": 0}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, p := loadWith(t); len(p) > 0 || c.MergeTrain != 0 {
		t.Errorf("turned off: %d, %v", c.MergeTrain, p)
	}
	if err := os.WriteFile(settings, []byte(`{"concurrent": 2, "merge_train": -1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "merge_train must be 0 (off) or more") {
		t.Errorf("a negative count: %v", p)
	}
}
