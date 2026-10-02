package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// ended is a dashboard whose run ended with ev an hour and 12 minutes after it began.
func ended(ev dispatch.Event) Dashboard {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m.began = time.Date(2026, 10, 2, 10, 35, 0, 0, time.Local)
	ev.Time = m.began.Add(72 * time.Minute)
	return runEvents(m, ev)
}

// On a terminal, a run that ended by itself closes with a headline for how it ended, the tickets
// and the time it took; a scoped run's SCOPE_ part follows on its own line.
func TestRunEndsWithAClosingLine(t *testing.T) {
	for _, tc := range []struct {
		text string
		n    int
		want string
	}{
		{"READY_EMPTY after 2 tickets", 2, "♪ Completed the Run  2 tickets · 1h12m\n"},
		{"LIMIT_REACHED at 40 tickets", 40, "♪ Reached the ticket limit (40)  40 tickets · 1h12m\n"},
		{"DRAINED after 1 tickets", 1, "♪ Stopped after the running tickets, as asked  1 ticket · 1h12m\n"},
		{"READY_EMPTY after 3 tickets; SCOPE_DONE: k-1 and its 2 subtickets are merged", 3,
			"♪ Completed the Run  3 tickets · 1h12m\n  SCOPE_DONE: k-1 and its 2 subtickets are merged\n"},
		{"READY_EMPTY after 0 tickets; SCOPE_OPEN: k-1: 1 of its 2 subtickets not done: k-2 (deferred; a b)", 0,
			"♪ Completed the Run  no tickets · 1h12m\n  SCOPE_OPEN: k-1: 1 of its 2 subtickets not done: k-2 (deferred; a b)\n"},
	} {
		var b strings.Builder
		Printer{Out: &b, Styled: true, Width: 200}.End(ended(dispatch.Event{Kind: dispatch.EvDone, N: tc.n, Limit: 40,
			Text: tc.text}))
		if got := ansi.Strip(b.String()); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.text, got, tc.want)
		}
	}
}

// The closing line is bold in the done colour; the scope's line is in the done colour when the
// scope is finished, and in the deferred colour, as work left, when it isn't.
func TestRunEndColours(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	const (
		boldGreen = "\x1b[1;38;2;73;222;128m"
		green     = "\x1b[38;2;73;222;128m"
		yellow    = "\x1b[38;2;250;204;21m"
	)
	for text, want := range map[string]string{
		"READY_EMPTY after 2 tickets":                      boldGreen + "♪ Completed the Run",
		"READY_EMPTY after 2 tickets; SCOPE_DONE: k-1 …":   green + "  SCOPE_DONE: k-1 …",
		"READY_EMPTY after 2 tickets; SCOPE_OPEN: k-1 …":   yellow + "  SCOPE_OPEN: k-1 …",
		"DRAINED after 2 tickets; SCOPE_UNREADABLE: k-1 …": yellow + "  SCOPE_UNREADABLE: k-1 …",
	} {
		var b strings.Builder
		Printer{Out: &b, Styled: true, Width: 200}.End(ended(dispatch.Event{Kind: dispatch.EvDone, N: 2, Text: text}))
		if !strings.Contains(b.String(), want) {
			t.Errorf("%s: printed %q, want %q in it", text, b.String(), want)
		}
	}
}

// What machines read keeps the loop's words: plain output prints the done line as the log has it,
// and a stop keeps its red line on a terminal.
func TestRunEndKeepsTheLogWordingOffTheTerminal(t *testing.T) {
	var b strings.Builder
	Printer{Out: &b}.End(ended(dispatch.Event{Kind: dispatch.EvDone, N: 2, Text: "READY_EMPTY after 2 tickets"}))
	if got := b.String(); !strings.HasSuffix(got, " 11:47:00 READY_EMPTY after 2 tickets\n") {
		t.Errorf("plain output printed %q", got)
	}
	b.Reset()
	Printer{Out: &b, Styled: true, Width: 200}.End(ended(dispatch.Event{Kind: dispatch.EvStop, Text: "PAUSED: k-1 idle"}))
	if got := ansi.Strip(b.String()); got != "11:47:00 ■ PAUSED: k-1 idle\n" {
		t.Errorf("a stop printed %q", got)
	}
	b.Reset()
	Printer{Out: &b, Styled: true}.End(Dashboard{}) // closed before the loop ended
	if b.Len() != 0 {
		t.Errorf("no final event printed %q", b.String())
	}
}

// A done event handed on after the dashboard closed gets the closing line, without the time taken.
func TestHandedOnRunEndHasTheClosingLine(t *testing.T) {
	var b strings.Builder
	Printer{Out: &b, Styled: true, Width: 200}.Event(dispatch.Event{Kind: dispatch.EvDone, N: 2, Limit: 40,
		Text: "LIMIT_REACHED at 2 tickets", Time: time.Now()})
	if got := ansi.Strip(b.String()); got != "♪ Reached the ticket limit (40)  2 tickets\n" {
		t.Errorf("printed %q", got)
	}
}

func TestRunTimeDropsTheZeroSecondsOfAWholeMinute(t *testing.T) {
	for d, want := range map[time.Duration]string{
		72 * time.Minute:                  "1h12m",
		18*time.Minute + 40*time.Second:   "18m40s",
		10 * time.Minute:                  "10m",
		45*time.Second + time.Millisecond: "45s",
		time.Hour + 50*time.Second:        "1h0m50s",
	} {
		if got := runTime(d); got != want {
			t.Errorf("runTime(%s) = %q, want %q", d, got, want)
		}
	}
}

// The organ phase's lines are sentences on a terminal, in a light purple rather than faint, for dark
// and light backgrounds alike; plain output keeps the log's lowercase wording. A warning is in the
// warning colour.
func TestSayIsALightSentenceOnATerminal(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	defer lipgloss.SetHasDarkBackground(true)
	for dark, lilac := range map[bool]string{true: "\x1b[38;2;195;181;253m", false: "\x1b[38;2;124;58;237m"} {
		lipgloss.SetHasDarkBackground(dark)
		var b strings.Builder
		Printer{Out: &b, Styled: true}.Say("finishing triage…", "Finishing triage…")
		got := b.String()
		if !strings.Contains(got, lilac+"Finishing triage…") || strings.Contains(got, "\x1b[2m") {
			t.Errorf("dark background %v: printed %q, want the text in %q, not faint", dark, got, lilac)
		}
		if ansi.Strip(got) != "◆ Finishing triage…\n" {
			t.Errorf("dark background %v: printed %q", dark, ansi.Strip(got))
		}
	}
	lipgloss.SetHasDarkBackground(true)
	var b strings.Builder
	Printer{Out: &b, Styled: true}.Warn("REVIEW_FAILED: claude: timed out")
	if got := b.String(); !strings.Contains(got, "\x1b[38;2;250;204;21mREVIEW_FAILED: claude: timed out") {
		t.Errorf("warning printed %q, want it in the warning colour", got)
	}
	b.Reset()
	p := Printer{Out: &b}
	p.Say("finishing triage…", "Finishing triage…")
	p.Warn("REVIEW_FAILED: claude: timed out")
	if got := b.String(); !strings.Contains(got, " finishing triage…\n") || !strings.Contains(got, " REVIEW_FAILED: claude: timed out\n") {
		t.Errorf("plain output printed %q", got)
	}
}
