//go:build darwin || linux

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
	"github.com/noesis-sol/orchestra/internal/project"
)

// A session on orchestra's own terminal, a spec's refinement or a feature's interview, starts claude
// with Chrome as the project's workers get it.
func TestTerminalSessionGetsTheWorkersChrome(t *testing.T) {
	t.Parallel()
	tty, _ := openTerminal(t)
	repo, tools := t.TempDir(), t.TempDir()
	faketool.Write(t, tools, "claude", "#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+tools+"/claude-args'\n")
	onPath(t, tools)
	var out strings.Builder
	chrome := []string{"--no-chrome"}
	if err := refineOnTerminal(testCtx(t.Context(), t), &stopWatch{},
		claudeSession{repo: repo, servers: sessionServers, chrome: chrome},
		"Break the spec down", project.RefineSpec, tty, tty, &out); err != nil {
		t.Fatalf("%v\n%s", err, out.String())
	}
	args := strings.Split(strings.TrimSuffix(read(t, filepath.Join(tools, "claude-args")), "\n"), "\n")
	checkSessionMCP(t, repo, args)
	checkSessionChrome(t, args, chrome)
}
