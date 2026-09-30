package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestConfigResolveConflicts(t *testing.T) {
	configFixture(t, `{"concurrent": 2}`)
	if c, p := loadWith(t); len(p) > 0 || !c.ResolveConflicts || c.ResolveTimeout != 20*time.Minute {
		t.Errorf("default: on %v, limit %s, %v", c.ResolveConflicts, c.ResolveTimeout, p)
	}
	if c, _ := loadWith(t, "--resolve-conflicts=false"); c.ResolveConflicts {
		t.Error("the flag turns it off")
	}
	os.WriteFile(".orchestra/settings.json", []byte(`{"concurrent": 2, "resolve_conflicts": false, "resolve_timeout": "5m"}`), 0o644)
	if c, p := loadWith(t); len(p) > 0 || c.ResolveConflicts || c.ResolveTimeout != 5*time.Minute {
		t.Errorf("settings: on %v, limit %s, %v", c.ResolveConflicts, c.ResolveTimeout, p)
	}
	if c, _ := loadWith(t, "--resolve-conflicts"); !c.ResolveConflicts {
		t.Error("the flag overrides settings")
	}
	os.WriteFile(".orchestra/settings.json", []byte(`{"concurrent": 2, "resolve_timeout": "soon"}`), 0o644)
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "resolve_timeout must be a positive duration") {
		t.Errorf("a bad limit: %v", p)
	}
}
