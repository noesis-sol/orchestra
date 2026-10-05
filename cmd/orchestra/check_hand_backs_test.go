package main

import (
	"os"
	"strings"
	"testing"
)

func TestConfigCheckHandBacks(t *testing.T) {
	configFixture(t, `{"concurrent": 2}`)
	if c, p := loadWith(t); len(p) > 0 || c.CheckHandBacks != 2 {
		t.Errorf("default: %d, %v", c.CheckHandBacks, p)
	}
	if err := os.WriteFile(".orchestra/settings.json", []byte(`{"concurrent": 2, "check_hand_backs": 0}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, p := loadWith(t); len(p) > 0 || c.CheckHandBacks != 0 {
		t.Errorf("turned off: %d, %v", c.CheckHandBacks, p)
	}
	if err := os.WriteFile(".orchestra/settings.json", []byte(`{"concurrent": 2, "check_hand_backs": -2}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "check_hand_backs must be 0 (never) or more") {
		t.Errorf("a negative count: %v", p)
	}
}
