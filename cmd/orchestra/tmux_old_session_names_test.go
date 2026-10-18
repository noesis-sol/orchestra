package main

import (
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// Before tmux 3.8 the session for --workspace foo.bar is foo_bar (3.2 to 3.6 rename it, 3.0, 3.1 and
// 3.7 refuse the name), so that is the session the hint attaches to; 3.8 keeps foo.bar.
func TestConfigTmuxHintNamesTheSessionOldTmuxMakes(t *testing.T) {
	for version, want := range map[string]string{
		"tmux 3.4": "tmux attach -t foo_bar", "tmux 3.7": "tmux attach -t foo_bar", "tmux 3.8": "tmux attach -t foo.bar:",
	} {
		configFixture(t, `{"terminal": "tmux"}`)
		dir := t.TempDir()
		faketool.Write(t, dir, "tmux", "#!/bin/sh\n[ \"$1\" = -V ] && { echo '"+version+"'; exit 0; }\nexit 1\n")
		onPath(t, dir)
		c, p := loadWith(t, "--workspace", "foo.bar")
		if len(p) > 0 {
			t.Fatalf("%s: problems: %v", version, p)
		}
		if c.Workspace != "foo.bar" || c.Watch != want {
			t.Errorf("%s: session %q, watch %q, want %q", version, c.Workspace, c.Watch, want)
		}
	}
}
