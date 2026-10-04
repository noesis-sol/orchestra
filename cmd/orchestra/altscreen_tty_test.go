//go:build darwin || linux

package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The dashboard draws on the alternate screen and leaves the normal one as it was: nothing clears
// it, before the dashboard or after. Once the dashboard has closed, the normal screen gets the run's
// summary, then the run's last line. This run reaches its ticket limit at once.
func TestDashboardDrawsOnTheAlternateScreen(t *testing.T) {
	nothingTools(t, backlog{})
	term, _, exit := runOnTerminal(t, "--tickets", "--done-so-far", "3", "--limit", "3")
	code, stderr := exit()
	if code != dispatch.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr:\n%s\nthe terminal:\n%s", code, stderr, screenOf(term))
	}
	const closing = "♪ Reached the ticket limit (3)"
	term.waitFor(t, closing) // what orchestra wrote has been read off the terminal
	raw := term.screen.raw()
	before, rest, opened := strings.Cut(raw, openAltScreen)
	dashboard, after, closed := strings.Cut(rest, closeAltScreen)
	if !opened || !closed {
		t.Fatalf("the dashboard didn't open and close the alternate screen:\n%q", raw)
	}
	if strings.Contains(before, eraseScreen) || strings.Contains(after, eraseScreen) {
		t.Errorf("the normal screen was cleared:\n%q", raw)
	}
	if !strings.Contains(ansi.Strip(dashboard), "Orchestra") {
		t.Errorf("the dashboard wasn't drawn on the alternate screen:\n%q", dashboard)
	}
	normal := strings.ReplaceAll(ansi.Strip(after), "\r\n", "\n")
	title, totals, last := strings.Index(normal, "Orchestra"), strings.Index(normal, "Completed"),
		strings.Index(normal, closing)
	if title < 0 || totals < title || last < totals {
		t.Errorf("after the dashboard, the normal screen should show the title, the totals, then %q:\n%s",
			closing, normal)
	}
}
