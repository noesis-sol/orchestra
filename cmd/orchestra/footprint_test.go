package main

import (
	"os"
	"testing"
)

func TestConfigFootprint(t *testing.T) {
	configFixture(t, `{"concurrent": 2}`)
	if c, p := loadWith(t); len(p) > 0 || c.NoFootprint {
		t.Errorf("default: footprint off %v, %v", c.NoFootprint, p)
	}
	if err := os.WriteFile(".orchestra/settings.json", []byte(`{"concurrent": 2, "footprint": false}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, p := loadWith(t); len(p) > 0 || !c.NoFootprint {
		t.Errorf("turned off: footprint off %v, %v", c.NoFootprint, p)
	}
	if err := os.WriteFile(".orchestra/settings.json", []byte(`{"concurrent": 2, "footprint": true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, p := loadWith(t); len(p) > 0 || c.NoFootprint {
		t.Errorf("turned on: footprint off %v, %v", c.NoFootprint, p)
	}
}
