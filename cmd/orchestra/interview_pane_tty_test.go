//go:build darwin || linux

package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// In a Herdr pane, orchestra splits its own pane (HERDR_PANE_ID) for the interview. When Herdr
// refuses the split, orchestra says why in one line and hands claude its terminal as before, the
// whole description as its first message, and what claude files runs as before.
func TestInterviewFallsBackToTheTerminalWhenHerdrCantSplit(t *testing.T) {
	dir := interviewTools(t, "f-1")
	t.Setenv("HERDR_PANE_ID", "w1:p1")
	term, _, exit := runOnTerminal(t)
	describeFeature(t, term)
	term.waitFor(t, "Start the run on f-1 (2 tickets)? [y/N]")
	term.typeKeys(t, "n\n", false)
	code, stderr := exit()
	if want := "orchestra couldn't open the interview in a pane beside its own, so claude takes this terminal: " +
		"herdr pane split w1:p1 --direction right --cwd "; code != dispatch.ExitOK || !strings.HasPrefix(stderr, want) ||
		!strings.HasSuffix(stderr, ": exit status 1: herdr: not in this test\n") || strings.Count(stderr, "\n") != 1 {
		t.Errorf("exit %d, stderr:\n%s", code, stderr)
	}
	if args := read(t, filepath.Join(dir, "claude-args")); !strings.HasSuffix(args,
		"[--]\n[- Add a --json flag\n- to the list command]\n") {
		t.Errorf("claude ran with:\n%s", args)
	}
	if out := screenOf(term); !strings.Contains(out, "Type /exit to come back.") || !strings.Contains(out, "fake claude: bye") {
		t.Errorf("claude didn't have the terminal:\n%s", out)
	}
}
