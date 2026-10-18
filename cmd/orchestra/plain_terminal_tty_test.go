//go:build darwin || linux

package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// On a terminal that can't draw the dashboard (TERM=dumb, which the forms read for their accessible
// form), the run prints its plain lines, as for a pipe: no alternate screen, no styled closing line.
func TestDumbTerminalRunPrintsPlainLines(t *testing.T) {
	nothingTools(t, backlog{})
	t.Setenv("TERM", "dumb")
	term, _, exit := runOnTerminal(t, "--tickets", "--done-so-far", "3", "--limit", "3")
	code, stderr := exit()
	if code != dispatch.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr:\n%s\nthe terminal:\n%s", code, stderr, screenOf(term))
	}
	raw := term.screen.raw()
	if strings.Contains(raw, openAltScreen) {
		t.Errorf("the dashboard opened the alternate screen:\n%q", raw)
	}
	if out := screenOf(term); !strings.Contains(out, "LIMIT_REACHED") || strings.Contains(out, "♪") {
		t.Errorf("want the plain LIMIT_REACHED line, not the styled closing:\n%s", out)
	}
}

// The dashboard reads its keys from stdin: a stdin that isn't a terminal gets plain lines, as a
// stdout that isn't one does, and so do --plain and TERM=dumb.
func TestPlainOutputWithoutTwoTerminals(t *testing.T) {
	tty, _ := openTerminal(t)
	pipeOut, pipeIn, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pipeOut.Close(); _ = pipeIn.Close() })
	env := func(term string) func(string) string {
		return func(k string) string {
			if k == "TERM" {
				return term
			}
			return ""
		}
	}
	for _, c := range []struct {
		name   string
		asked  bool
		term   string
		stdin  io.Reader
		stdout io.Writer
		plain  bool
	}{
		{"terminals", false, "xterm-256color", tty, tty, false},
		{"--plain", true, "xterm-256color", tty, tty, true},
		{"TERM=dumb", false, "dumb", tty, tty, true},
		{"stdin a pipe", false, "xterm-256color", pipeOut, tty, true},
		{"stdout a pipe", false, "xterm-256color", tty, pipeIn, true},
		{"stdout a buffer", false, "xterm-256color", tty, &strings.Builder{}, true},
	} {
		if got := plainOutput(c.asked, env(c.term), c.stdin, c.stdout); got != c.plain {
			t.Errorf("%s: plain %v, want %v", c.name, got, c.plain)
		}
	}
}
