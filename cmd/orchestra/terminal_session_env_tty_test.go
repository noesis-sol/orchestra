//go:build darwin || linux

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
)

// A session on orchestra's own terminal, started by an orchestra run from a Claude Code session, runs
// as a session of its own, not as that one's child: its claude gets none of the session's markers.
func TestTerminalSessionLeavesOutAClaudeSession(t *testing.T) {
	t.Parallel()
	tty, _ := openTerminal(t)
	repo, tools := t.TempDir(), t.TempDir()
	faketool.Write(t, tools, "claude", "#!/bin/sh\n"+
		`echo "${CLAUDECODE-unset} ${CLAUDE_CODE_ENTRYPOINT-unset} ${CLAUDE_CODE_CHILD_SESSION-unset}" > '`+
		tools+"/claude-env'\n")
	onPath(t, tools)
	for _, v := range []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_CHILD_SESSION"} {
		setenv(t, v, "1")
	}
	var out strings.Builder
	if err := refineOnTerminal(runCtx(t), &stopWatch{}, claudeSession{repo: repo, servers: sessionServers},
		"Break the spec down", project.RefineSpec, tty, tty, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	if got := read(t, filepath.Join(tools, "claude-env")); got != "unset unset unset\n" {
		t.Errorf("claude's session markers: %q, want them unset", got)
	}
}
