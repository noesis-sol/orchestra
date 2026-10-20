//go:build darwin || linux

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/faketool"
)

// On a terminal, the loop started on the environment ticket during the breakdown prints its lines
// as they come, until the breakdown is over; the dashboard then opens with them, and the run's
// summary after it lists the ticket the loop took up before it opened.
func TestSpecRunOnATerminalStartsDuringTheBreakdown(t *testing.T) {
	t.Parallel()
	tools := t.TempDir()
	tabs := filepath.Join(tools, "tabs")
	faketool.Write(t, tools, "bd", earlyBd)
	// Herdr has no earlier worker in a tab, and opens none: s-1.1's start fails, which stops the run.
	faketool.Write(t, tools, "herdr", `#!/bin/sh
case "$1 $2" in
"agent get") echo '{"error":{"code":"agent_not_found","message":"no agent"}}' >&2 ;;
"tab create")
	while [ $# -gt 0 ]; do [ "$1" = --label ] && echo "$2" >> '`+tabs+`'; shift; done
	echo '{"error":{"code":"workspace_not_found","message":"no workspace"}}' >&2 ;;
*) echo 'herdr: not in this test' >&2 ;;
esac
exit 1
`)
	faketool.Write(t, tools, "claude", "#!/bin/sh\necho 'claude: not in this test' >&2\nexit 1\n")
	onPath(t, tools)
	for _, k := range []string{"ORCHESTRA_SPEC", "ORCHESTRA_REFINE"} {
		setenv(t, k, "")
	}
	spec := writeSpec(t, filepath.Join(t.TempDir(), "app.md"), "# An app\n\nIt lists things.\n")
	var f *earlyBreakdown
	breakDownWith(t, func(s *session) specBreakdown {
		f = &earlyBreakdown{repo: s.cfg.Repo, setups: tabs}
		return f
	})
	term, _, exit := runOnTerminal(t, "--spec", spec)
	code, stderr := exit()
	out := screenOf(term)
	if code == 0 || f == nil || !f.tookUp {
		t.Fatalf("exit %d (took s-1.1 up: %v), stderr:\n%s\nthe terminal:\n%s", code, f != nil && f.tookUp, stderr, out)
	}
	at := 0
	for _, step := range []string{"Starting on s-1.1, the environment ticket, while the breakdown files the rest",
		"Set up the project", "broke the spec down into s-1 (1 epic, 2 tickets)", "Tickets", "s-1.1"} {
		i := strings.Index(out[at:], step)
		if i < 0 {
			t.Fatalf("the terminal lacks %q after the steps before it:\n%s", step, out)
		}
		at += i + len(step)
	}
}
