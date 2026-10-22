package main

import (
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// tmux reads a target foo:bar as session foo, window bar, so a session named foo:bar can't be
// attached to by name: --workspace foo:bar is the session foo_bar on every tmux, 3.8 included,
// where only the session orchestra runs in keeps its ':', in sight and needing no hint.
func TestConfigTmuxWorkspaceWithColonIsRenamed(t *testing.T) {
	t.Parallel()
	for _, version := range []string{"tmux 3.4", "tmux 3.8"} {
		configFixture(t, `{"terminal": "tmux"}`)
		dir := t.TempDir()
		faketool.Write(t, dir, "tmux", `#!/bin/sh
[ "$1" = -V ] && { echo '`+version+`'; exit 0; }
[ "$1" = -u ] && shift
[ "$1" = display-message ] && { echo 'foo:bar'; exit 0; }
exit 1
`)
		onPath(t, dir)
		c, p := loadWith(t, "--workspace", "foo:bar.baz")
		if len(p) > 0 {
			t.Fatalf("%s: problems: %v", version, p)
		}
		if c.Workspace != "foo_bar_baz" || c.Watch != "tmux attach -t foo_bar_baz" || c.where.inSight {
			t.Errorf("%s: session %q, watch %q, in sight %v", version, c.Workspace, c.Watch, c.where.inSight)
		}

		setenv(t, "HERDR_ENV", "")
		setenv(t, "TMUX", "/tmp/tmux-501/default,1,0")
		setenv(t, "TMUX_PANE", "%4")
		for _, args := range [][]string{nil, {"--workspace", "foo:bar"}} {
			c, p = loadWith(t, args...)
			if len(p) > 0 {
				t.Fatalf("%s %v: problems: %v", version, args, p)
			}
			if c.Workspace != "foo:bar" || c.Watch != "" || !c.where.inSight {
				t.Errorf("%s %v: session %q, watch %q, in sight %v", version, args, c.Workspace, c.Watch, c.where.inSight)
			}
		}
	}
}
