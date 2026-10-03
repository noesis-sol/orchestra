//go:build darwin || linux

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// raw is what was drawn, colours and control sequences included.
func (s *screen) raw() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// clearScreen is what the dashboard clears the screen with before it opens.
const clearScreen = "\x1b[H\x1b[2J"

// In a terminal the question says whether anything is ready; picking the current tickets with
// nothing to run shows why in a box and exits 0, without the dashboard.
func TestQuestionWithNothingToRunShowsWhy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		b      backlog
		option string
		box    []string
	}{
		{"all done", backlog{unclosed: `[{"id":"k-e","status":"open","issue_type":"epic"}]`},
			"> Current tickets: none, all done", []string{"╭", "│ ✓ All done", "Still open: k-e (bd close k-e)", "╰"}},
		{"none ready", backlog{unclosed: `[{"id":"k-1","status":"open","issue_type":"task"},` +
			`{"id":"k-2","status":"in_progress","issue_type":"task"}]`},
			"> Current tickets: none ready (2 open)",
			[]string{"╭", "│ ○ Nothing ready to run", "1 ticket waits on other tickets: bd blocked", "1 in progress", "╰"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := nothingTools(t, tc.b)
			term, repo, exit := runOnTerminal(t)
			term.waitFor(t, tc.option)
			term.typeKeys(t, keyEnter, false)
			code, stderr := exit()
			if code != dispatch.ExitOK || stderr != "" {
				t.Fatalf("exit %d, stderr:\n%s\nthe terminal:\n%s", code, stderr, screenOf(term))
			}
			out := screenOf(term)
			for _, want := range tc.box {
				if !strings.Contains(out, want) {
					t.Errorf("the terminal lacks %q:\n%s", want, out)
				}
			}
			if strings.Contains(term.screen.raw(), clearScreen) || strings.Contains(out, "START") {
				t.Errorf("the dashboard opened:\n%s", out)
			}
			if got := kindsOf(streamRecords(t, repo)); got != "start done end" {
				t.Errorf("records: %s", got)
			}
			noCalls(t, dir)
		})
	}
}

// With tickets ready the option counts them, as before.
func TestQuestionCountsTheReadyTickets(t *testing.T) {
	nothingTools(t, backlog{ready: `[{"id":"k-1","status":"open","issue_type":"task"},` +
		`{"id":"k-2","status":"open","issue_type":"task"}]`})
	term, _, exit := runOnTerminal(t)
	term.waitFor(t, "> Current tickets: 2 ready")
	term.typeKeys(t, keyEsc, false)
	if code, _ := exit(); code != dispatch.ExitInterrupted {
		t.Errorf("exit %d after Esc", code)
	}
}

// A run that doesn't ask, on a terminal, shows only the box, in place of the dashboard: the screen
// isn't cleared.
func TestNothingToRunOnATerminalShowsTheBox(t *testing.T) {
	dir := nothingTools(t, backlog{})
	term, repo, exit := runOnTerminal(t, "--tickets")
	code, stderr := exit()
	if code != dispatch.ExitOK || stderr != "" {
		t.Fatalf("exit %d, stderr:\n%s\nthe terminal:\n%s", code, stderr, screenOf(term))
	}
	const want = "╭────────────╮\n│ ✓ All done │\n╰────────────╯\n"
	for deadline := time.Now().Add(10 * time.Second); screenOf(term) != want && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond) // until what orchestra wrote has been read off the terminal
	}
	if out := screenOf(term); out != want {
		t.Errorf("the terminal:\n%q\nwant only the box:\n%q", out, want)
	}
	if strings.Contains(term.screen.raw(), clearScreen) {
		t.Errorf("the screen was cleared:\n%q", term.screen.raw())
	}
	if got := kindsOf(streamRecords(t, repo)); got != "start done end" {
		t.Errorf("records: %s", got)
	}
	noCalls(t, dir)
}
