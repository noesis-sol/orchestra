package tui

import (
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// attacks are text a prompt-injected worker or organ could put in a title: a clipboard write (OSC
// 52), a cleared screen (CSI 2J) and a window title (OSC 0), with other control characters.
var attacks = map[string]string{
	"OSC 52": "Fix \x1b]52;c;cm0gLXJmIH4K\x07it",
	"CSI 2J": "Fix \x1b[2J\x1b[Hit\b\x00",
	"OSC 0":  "Fix \x1b]0;pwned\x1b\\it\x9b\u009b",
}

// sgr matches orchestra's own styling: colours and bold.
var sgr = regexp.MustCompile("\x1b\\[[0-9;]*m")

// checkPrintable fails t for out with a control character other than a line break, once
// orchestra's own styling is taken out, or without the title's words.
func checkPrintable(t *testing.T, where, out string) {
	t.Helper()
	plain := sgr.ReplaceAllString(out, "")
	for _, r := range plain {
		if r != '\n' && unicode.IsControl(r) {
			t.Errorf("%s: %q came through:\n%q", where, r, out)
			return
		}
	}
	if !strings.Contains(plain, "Fix it") {
		t.Errorf("%s: the title's words are missing:\n%q", where, plain)
	}
}

// styled sets lipgloss to colour its output for the rest of t, so that a test sees orchestra's own
// styling kept.
func styled(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })
}

// The dashboard shows a title, a triage summary and a worker's activity without their escape
// sequences, in the tickets table, the worker's box and the worker list, and keeps its own colours.
func TestDashboardDropsControlSequences(t *testing.T) {
	styled(t)
	for name, text := range attacks {
		m := runEvents(reviewDashboard(),
			dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: text},
			dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "k-2", Title: "Second"},
			dispatch.Event{Kind: dispatch.EvDeferred, Ticket: "k-2", Detail: "still open"},
			dispatch.Event{Kind: dispatch.EvTriage, Ticket: "k-2", Detail: "retry", Title: text})
		next, _ := m.Update(statusMsg(dispatch.Status{Ticket: "k-1", Title: text, Started: time.Now(),
			Agent: dispatch.StateWorking, Activity: text}))
		m = next.(Dashboard)
		view := m.View()
		checkPrintable(t, name+" in the view", view)
		if !sgr.MatchString(view) {
			t.Errorf("%s: the view lost its colours:\n%q", name, view)
		}
		checkPrintable(t, name+" in the tickets table", m.ticketsTable(m.width, 100))
		checkPrintable(t, name+" in the worker list", m.workerList(m.width))
		checkPrintable(t, name+" in the worker's box", m.workerPanels(m.width))
	}
}

// Printed lines, plain and styled, show an event's title and text without their escape sequences;
// the styled ones keep their colours.
func TestPrinterDropsControlSequences(t *testing.T) {
	styled(t)
	at := time.Date(2026, 10, 6, 9, 0, 0, 0, time.Local)
	for name, text := range attacks {
		for _, ev := range []dispatch.Event{
			{Time: at, Kind: dispatch.EvDispatch, N: 1, Limit: 4, Ticket: "k-1", Title: text, Text: "DISPATCH k-1 " + text},
			{Time: at, Kind: dispatch.EvTriage, Ticket: "k-1", Detail: "retry", Title: text, Text: "TRIAGE k-1 " + text},
			{Time: at, Kind: dispatch.EvWarn, Ticket: "k-1", Text: "WARN " + text},
		} {
			var plain, term strings.Builder
			Printer{Out: &plain}.Event(ev)
			checkPrintable(t, name+" printed plain", plain.String())
			Printer{Out: &term, Styled: true, Width: 200}.Event(ev)
			checkPrintable(t, name+" printed styled", term.String())
			if !sgr.MatchString(term.String()) {
				t.Errorf("%s: the styled line lost its colours:\n%q", name, term.String())
			}
		}
		var b strings.Builder
		p := Printer{Out: &b}
		p.Warn("REVIEW_FAILED: " + text)
		p.Say("report not saved: "+text, "Report not saved: "+text)
		p.Report("# " + text)
		checkPrintable(t, name+" said, warned and reported", b.String())
	}
}

// The interview's line names the filed epic's title without its escape sequences.
func TestInterviewLineDropsControlSequences(t *testing.T) {
	styled(t)
	for name, text := range attacks {
		line := InterviewProgress{Epic: "k-9", Title: text, Tickets: 3}.Line(80)
		checkPrintable(t, name+" in the interview's line", line)
		if !sgr.MatchString(line) {
			t.Errorf("%s: the interview's line lost its colours:\n%q", name, line)
		}
	}
}

// Printable keeps line breaks and tabs, turns a carriage return into a line break and drops bytes
// that aren't UTF-8.
func TestPrintableKeepsLayout(t *testing.T) {
	for in, want := range map[string]string{
		"a\nb\tc":            "a\nb\tc",
		"a\r\nb\rc":          "a\nb\nc",
		"a\xffb":             "ab",
		"\x1b[31mred\x1b[0m": "red",
		"naïve ☃":            "naïve ☃",
	} {
		if got := Printable(in); got != want {
			t.Errorf("Printable(%q) = %q, want %q", in, got, want)
		}
	}
}
