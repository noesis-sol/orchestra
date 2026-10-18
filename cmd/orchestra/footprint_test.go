package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigFootprint(t *testing.T) {
	t.Parallel()
	configFixture(t, `{"concurrent": 2}`)
	if c, p := loadWith(t); len(p) > 0 || c.NoFootprint {
		t.Errorf("default: footprint off %v, %v", c.NoFootprint, p)
	}
	if err := os.WriteFile(filepath.Join(workDir(t), ".orchestra", "settings.json"), []byte(`{"concurrent": 2, "footprint": false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, p := loadWith(t); len(p) > 0 || !c.NoFootprint {
		t.Errorf("turned off: footprint off %v, %v", c.NoFootprint, p)
	}
	if err := os.WriteFile(filepath.Join(workDir(t), ".orchestra", "settings.json"), []byte(`{"concurrent": 2, "footprint": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, p := loadWith(t); len(p) > 0 || c.NoFootprint {
		t.Errorf("turned on: footprint off %v, %v", c.NoFootprint, p)
	}
}
