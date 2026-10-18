package main

import (
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// A tmux older than 3.0 is a setup problem naming its version; one tmux -V doesn't answer isn't.
func TestConfigTmuxTooOld(t *testing.T) {
	t.Parallel()
	configFixture(t, `{"terminal": "tmux"}`)
	dir := t.TempDir()
	faketool.Write(t, dir, "tmux", "#!/bin/sh\n[ \"$1\" = -V ] && { echo 'tmux 2.9a'; exit 0; }\nexit 1\n")
	onPath(t, dir)
	_, p := loadWith(t)
	if len(p) != 1 || !strings.Contains(p[0], "tmux 2.9a is too old: orchestra needs tmux 3.0 or later") {
		t.Errorf("tmux 2.9a: problems %v", p)
	}
	fakeTmux(t)
	if _, p := loadWith(t); len(p) > 0 {
		t.Errorf("tmux -V failing: problems %v", p)
	}
}
