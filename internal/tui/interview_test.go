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

// While the interview runs in the pane beside orchestra's, orchestra's pane says in words what
// claude is doing, as Herdr reads it, and how long the interview has run; once claude has filed the
// feature, the epic, its title and how many tickets it has, whatever claude does meanwhile.
func TestInterviewProgressLine(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    InterviewProgress
		want string
	}{
		{"working", InterviewProgress{Agent: dispatch.StateWorking, Elapsed: 83*time.Second + 600*time.Millisecond},
			"claude is working  1m23s"},
		{"blocked", InterviewProgress{Agent: dispatch.StateBlocked, Elapsed: 5 * time.Second},
			"claude is waiting for a permission answer  5s"},
		{"idle", InterviewProgress{Agent: dispatch.StateIdle, Elapsed: 2 * time.Minute},
			"claude is waiting for you  2m0s"},
		{"done", InterviewProgress{Agent: dispatch.StateDone, Elapsed: time.Hour + time.Second},
			"claude is waiting for you  1h0m1s"},
		{"unknown", InterviewProgress{Agent: dispatch.StateUnknown, Elapsed: 4 * time.Second},
			"claude is running  4s"},
		{"not read yet", InterviewProgress{Elapsed: time.Second}, "claude is running  1s"},
		{"filed, bd not read yet", InterviewProgress{Agent: dispatch.StateWorking, Elapsed: time.Minute, Epic: "f-1"},
			"Filed f-1"},
		{"filed", InterviewProgress{Agent: dispatch.StateIdle, Elapsed: time.Minute, Epic: "f-1", Title: "JSON output",
			Tickets: 2}, "Filed f-1: JSON output (2 tickets)"},
		{"filed, one ticket", InterviewProgress{Epic: "f-1", Title: "JSON\noutput", Tickets: 1},
			"Filed f-1: JSON output (1 ticket)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ansi.Strip(tc.p.Line(80)); got != tc.want {
				t.Errorf("line = %q, want %q", got, tc.want)
			}
		})
	}
}

// orchestra's pane is half its width while the interview's is open: the line is cut to it, the
// epic's title first, so that the number of tickets stays in view.
func TestInterviewProgressLineFitsThePane(t *testing.T) {
	filed := InterviewProgress{Epic: "orchestra-cv4", Title: "Talk a typed feature through in a Herdr pane beside " +
		"orchestra", Tickets: 4}
	for _, tc := range []struct {
		p     InterviewProgress
		width int
		want  string
	}{
		{filed, 60, "Filed orchestra-cv4: Talk a typed feature throu… (4 tickets)"},
		{filed, 35, "Filed orchestra-cv4: T… (4 tickets)"},
		{filed, 20, "Filed orchestra-cv4"},
		{InterviewProgress{Agent: dispatch.StateBlocked, Elapsed: 3 * time.Minute}, 30, "claude is waiting for a permi…"},
	} {
		got := tc.p.Line(tc.width)
		if w := ansi.StringWidth(got); w > tc.width {
			t.Errorf("%d columns wide in %d: %q", w, tc.width, ansi.Strip(got))
		}
		if ansi.Strip(got) != tc.want {
			t.Errorf("in %d columns: %q, want %q", tc.width, ansi.Strip(got), tc.want)
		}
	}
}

// The line takes the dashboard's colours: a working claude in cyan, one that needs an answer in
// red, one waiting for the user's next message in yellow, the time faint, the filed epic in green.
func TestInterviewProgressColours(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	for _, tc := range []struct {
		p    InterviewProgress
		want []string
	}{
		{InterviewProgress{Agent: dispatch.StateWorking}, []string{pickedStyle.Render("claude is working"),
			dimStyle.Render("0s")}},
		{InterviewProgress{Agent: dispatch.StateBlocked}, []string{stopStyle.Render("claude is waiting for a permission answer")}},
		{InterviewProgress{Agent: dispatch.StateDone}, []string{deferredStyle.Render("claude is waiting for you")}},
		{InterviewProgress{Epic: "f-1", Title: "JSON output", Tickets: 2}, []string{closedStyle.Render("Filed f-1"),
			dimStyle.Render(" (2 tickets)")}},
	} {
		got := tc.p.Line(80)
		for _, want := range tc.want {
			if !strings.Contains(got, want) {
				t.Errorf("%q lacks %q", got, want)
			}
		}
	}
}

// The line is drawn in place at the cursor, each time over the last, one column short of the
// terminal's width, and cleared to leave the cursor where the first one started.
func TestInterviewLineDrawsInPlace(t *testing.T) {
	var out strings.Builder
	width := 30
	l := &InterviewLine{Out: &out, Width: func() int { return width }}
	l.Clear()
	if out.Len() != 0 {
		t.Fatalf("Clear with no line drawn wrote %q", out.String())
	}
	l.Show(InterviewProgress{Agent: dispatch.StateWorking, Elapsed: time.Second})
	width = 20
	l.Show(InterviewProgress{Agent: dispatch.StateBlocked, Elapsed: 2 * time.Second})
	l.Clear()
	l.Clear()
	const erase = "\r" + ansi.EraseLineRight
	if want := erase + "claude is working  1s" + erase + "claude is waiting f…" + erase; out.String() != want {
		t.Errorf("drew %q, want %q", out.String(), want)
	}
}
