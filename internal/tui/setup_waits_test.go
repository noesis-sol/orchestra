package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Ready tickets waiting on a setup that failed in their worktrees follow the queue's count apart
// from those held back otherwise, as "· 1 setup failed", in the strip and the one-line form alike.
func TestTheTotalsCountTheTicketsWaitingOnAFailedSetup(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "main"}, func() {}, func(bool) {}, func(string) {})
	m.height = 40
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvQueue, Queued: 0, Unclosed: 29,
		Held: dispatch.HeldBack{Merge: 2, Setup: 1}})
	for w, want := range map[int]string{120: "0 of 29 · 2 held back · 1 setup failed",
		72: "queue 0 of 29 · 2 held back · 1 setup failed"} {
		m.width = w
		if plain := ansi.Strip(m.View()); !strings.Contains(plain, want) {
			t.Errorf("width %d: no %q in\n%s", w, want, plain)
		}
	}

	m = runEvents(m, dispatch.Event{Kind: dispatch.EvQueue, Queued: 0, Unclosed: 29,
		Held: dispatch.HeldBack{Setup: 2}})
	m.width = 120
	if plain := ansi.Strip(m.View()); strings.Contains(plain, "held back") || !strings.Contains(plain, "0 of 29 · 2 setup failed") {
		t.Errorf("only setups failed, yet\n%s", plain)
	}
}

// The run's closing names the tickets waiting on a failed setup on a line of its own, in the deferred
// colour, as work left, before a scoped run's SCOPE_ part.
func TestRunEndSaysWhichTicketsWaitOnAFailedSetup(t *testing.T) {
	waits := "SETUP_WAITS: k-2 waits on the setup that failed in its worktree (the whole output is in /wt/k-2/" +
		".orchestra/run/setup.log); fix the setup, then run again"
	for text, want := range map[string]string{
		"READY_EMPTY after 2 tickets; " + waits: "♪ Completed the Run  2 tickets · 1h12m\n  " + waits + "\n",
		"READY_EMPTY after 2 tickets; " + waits + "; SCOPE_OPEN: k-1: …": "♪ Completed the Run  2 tickets · 1h12m\n  " +
			waits + "\n  SCOPE_OPEN: k-1: …\n",
	} {
		var b strings.Builder
		Printer{Out: &b, Styled: true, Width: 400}.End(ended(t, dispatch.Event{Kind: dispatch.EvDone, N: 2, Text: text}))
		if got := ansi.Strip(b.String()); got != want {
			t.Errorf("%s:\n got %q\nwant %q", text, got, want)
		}
	}

	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	var b strings.Builder
	Printer{Out: &b, Styled: true, Width: 400}.End(ended(t, dispatch.Event{Kind: dispatch.EvDone, N: 2,
		Text: "READY_EMPTY after 2 tickets; " + waits}))
	if yellow := "\x1b[38;2;250;204;21m  SETUP_WAITS: k-2"; !strings.Contains(b.String(), yellow) {
		t.Errorf("printed %q, want %q in it", b.String(), yellow)
	}
}
