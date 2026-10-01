package main

import (
	"os"
	"strings"
	"testing"
)

// Claude workers start at the effort the project sets, which --worker-effort overrides; without
// either they get no --effort and run at Claude Code's default. The organs' effort works the same
// way, and a level claude doesn't know is a setup problem.
func TestConfigEffort(t *testing.T) {
	configFixture(t, `{"mcp_servers": []}`)
	if c, p := loadWith(t); len(p) > 0 || strings.Join(c.WorkerArgs, " ") != "--no-chrome" || c.OrganEffort != "" {
		t.Errorf("unset: worker arguments %q, organ effort %q, problems %v", c.WorkerArgs, c.OrganEffort, p)
	}
	if err := os.WriteFile(".orchestra/settings.json",
		[]byte(`{"mcp_servers": [], "worker_effort": "medium", "organ_effort": "high"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, p := loadWith(t); len(p) > 0 || strings.Join(c.WorkerArgs, " ") != "--no-chrome --effort medium" ||
		c.OrganEffort != "high" {
		t.Errorf("settings: worker arguments %q, organ effort %q, problems %v", c.WorkerArgs, c.OrganEffort, p)
	}
	c, p := loadWith(t, "--worker-effort", "xhigh", "--organ-effort", "low")
	if len(p) > 0 || strings.Join(c.WorkerArgs, " ") != "--no-chrome --effort xhigh" || c.OrganEffort != "low" {
		t.Errorf("flags: worker arguments %q, organ effort %q, problems %v", c.WorkerArgs, c.OrganEffort, p)
	}
	t.Setenv("WORKER_EFFORT", "max")
	if c, _ := loadWith(t); strings.Join(c.WorkerArgs, " ") != "--no-chrome --effort max" {
		t.Errorf("WORKER_EFFORT: worker arguments %q", c.WorkerArgs)
	}
	t.Setenv("WORKER_EFFORT", "")

	if _, p := loadWith(t, "--worker-effort", "extreme"); len(p) != 1 || !strings.Contains(p[0], "--worker-effort must be one of") {
		t.Errorf("bad flag: %v", p)
	}
	if err := os.WriteFile(".orchestra/settings.json", []byte(`{"organ_effort": "hi"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, p := loadWith(t); len(p) != 1 || !strings.Contains(p[0], "organ_effort must be one of low, medium, high, xhigh, max") {
		t.Errorf("bad setting: %v", p)
	}
}
