package main

import "testing"

// A session named with '.' is attached to as "name:": tmux reads "foo.bar" as window "foo", pane "bar".
func TestConfigTmuxSessionNamedWithADotIsAttachedToAsASession(t *testing.T) {
	configFixture(t, `{"terminal": "tmux"}`)
	fakeTmux(t)
	c, p := loadWith(t, "--workspace", "foo.bar")
	if len(p) > 0 {
		t.Fatalf("problems: %v", p)
	}
	if c.Workspace != "foo.bar" || c.Watch != "tmux attach -t foo.bar:" {
		t.Errorf("session %q, watch %q", c.Workspace, c.Watch)
	}
}
