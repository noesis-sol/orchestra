package tui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The message a run with nothing to run shows: in a box on a terminal, as plain lines otherwise.

// nothingCases are the messages, each with its box on an 80-column terminal (ANSI stripped) and
// its plain lines (without their times).
var nothingCases = []struct {
	name   string
	n      dispatch.NothingToRun
	styled string
	plain  string
}{
	{
		name: "all done",
		n:    dispatch.NothingToRun{AllDone: true},
		styled: "" +
			"╭────────────╮\n" +
			"│ ✓ All done │\n" +
			"╰────────────╯\n",
		plain: "✓ All done\n",
	},
	{
		name: "all done, epics still open",
		n:    dispatch.NothingToRun{AllDone: true, StillOpen: []string{"k-1", "k-7"}},
		styled: "" +
			"╭───────────────────────────────────────────╮\n" +
			"│ ✓ All done                                │\n" +
			"│   Still open: k-1, k-7 (bd close k-1 k-7) │\n" +
			"╰───────────────────────────────────────────╯\n",
		plain: "✓ All done\n  Still open: k-1, k-7 (bd close k-1 k-7)\n",
	},
	{
		name: "nothing ready",
		n:    dispatch.NothingToRun{Questions: 2, Waiting: 1, InProgress: 3, Other: 1, Unmerged: 1},
		styled: "" +
			"╭─────────────────────────────────────────────────────╮\n" +
			"│ ○ Nothing ready to run                              │\n" +
			"│   2 questions wait for your answer: bd human list   │\n" +
			"│   1 ticket waits on other tickets: bd blocked       │\n" +
			"│   3 in progress · 1 other                           │\n" +
			"│   1 closed but not merged: bd list --label unmerged │\n" +
			"╰─────────────────────────────────────────────────────╯\n",
		plain: "○ Nothing ready to run\n" +
			"  2 questions wait for your answer: bd human list\n" +
			"  1 ticket waits on other tickets: bd blocked\n" +
			"  3 in progress · 1 other\n" +
			"  1 closed but not merged: bd list --label unmerged\n",
	},
	{
		name: "nothing ready in a scope",
		n: dispatch.NothingToRun{Scope: "k-1", NotDone: []dispatch.NotDone{
			{ID: "k-2", Why: "blocked by k-9 outside the scope"},
			{ID: "k-3", Why: "closed but not merged: left unmerged by an earlier run"}}},
		styled: "" +
			"╭────────────────────────────────────────────────────────────────╮\n" +
			"│ ○ Nothing under k-1 is ready to run                            │\n" +
			"│   k-2 (blocked by k-9 outside the scope)                       │\n" +
			"│   k-3 (closed but not merged: left unmerged by an earlier run) │\n" +
			"╰────────────────────────────────────────────────────────────────╯\n",
		plain: "○ Nothing under k-1 is ready to run\n" +
			"  k-2 (blocked by k-9 outside the scope)\n" +
			"  k-3 (closed but not merged: left unmerged by an earlier run)\n",
	},
}

func TestNothingIsABoxOnATerminal(t *testing.T) {
	for _, tc := range nothingCases {
		var b strings.Builder
		Printer{Out: &b, Styled: true, Width: 80}.Nothing(tc.n)
		if got := ansi.Strip(b.String()); got != tc.styled {
			t.Errorf("%s:\n%s\nwant\n%s", tc.name, got, tc.styled)
		}
	}
}

// timed is a plain line's time, as Say prints it.
var timed = regexp.MustCompile(`(?m)^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d `)

func TestNothingIsPlainLinesOffTheTerminal(t *testing.T) {
	for _, tc := range nothingCases {
		var b strings.Builder
		Printer{Out: &b}.Nothing(tc.n)
		got := b.String()
		if n := len(timed.FindAllString(got, -1)); n != strings.Count(got, "\n") {
			t.Errorf("%s: %d of the lines have a time:\n%s", tc.name, n, got)
		}
		if got := timed.ReplaceAllString(got, ""); got != tc.plain {
			t.Errorf("%s:\n%s\nwant\n%s", tc.name, got, tc.plain)
		}
		if got != ansi.Strip(got) {
			t.Errorf("%s: plain output has colour: %q", tc.name, got)
		}
	}
}

// One of each count reads in the singular; deferred joins the in-progress line.
func TestNothingReadyCountsInTheSingular(t *testing.T) {
	head, rest := nothingLines(dispatch.NothingToRun{Questions: 1, Waiting: 2, Deferred: 4}, false)
	want := []string{"1 question waits for your answer: bd human list", "2 tickets wait on other tickets: bd blocked",
		"4 deferred"}
	if head != "○ Nothing ready to run" || strings.Join(rest, "\n") != strings.Join(want, "\n") {
		t.Errorf("got %q %q, want %q", head, rest, want)
	}
}

// A narrow terminal wraps the box's long lines, indented under their own.
func TestNothingBoxWrapsToTheTerminal(t *testing.T) {
	n := dispatch.NothingToRun{Scope: "k-1", NotDone: []dispatch.NotDone{
		{ID: "k-3", Why: "closed but not merged: left unmerged by an earlier run"}}}
	got := ansi.Strip(nothingBox(n, 40))
	want := "" +
		"╭──────────────────────────────────────╮\n" +
		"│ ○ Nothing under k-1 is ready to run  │\n" +
		"│   k-3 (closed but not merged: left   │\n" +
		"│     unmerged by an earlier run)      │\n" +
		"╰──────────────────────────────────────╯"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	for _, l := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(l); w > 40 {
			t.Errorf("%d columns, wider than the terminal: %q", w, l)
		}
	}
}

// All done has a green ✓ and a green border; nothing ready a faint ○ and a grey border.
func TestNothingColours(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	const (
		boldGreen = "\x1b[1;38;2;73;222;128m"
		green     = "\x1b[38;2;73;222;128m"
		grey      = "\x1b[38;2;107;113;128m"
		faint     = "\x1b[2m"
	)
	done := nothingBox(dispatch.NothingToRun{AllDone: true}, 80)
	if !strings.Contains(done, boldGreen+"✓") || !strings.Contains(done, green+"╭") {
		t.Errorf("all done: %q, want a green ✓ and border", done)
	}
	ready := nothingBox(dispatch.NothingToRun{Waiting: 1}, 80)
	if !strings.Contains(ready, faint+"○") || !strings.Contains(ready, grey+"╭") {
		t.Errorf("nothing ready: %q, want a faint ○ and a grey border", ready)
	}
}
