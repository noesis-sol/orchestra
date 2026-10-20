package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The dark-background colours of the winding-down line's squares, as lipgloss writes them.
const (
	amberSquare = "\x1b[38;2;251;146;60m■\x1b[0m"  // still running
	greenSquare = "\x1b[38;2;73;222;128m■\x1b[0m"  // merged
	redSquare   = "\x1b[38;2;248;113;113m■\x1b[0m" // set aside
)

// windingDown is a dashboard with four workers running, winding down as confirmed with s and y.
func windingDown(t *testing.T) Dashboard {
	f := run(t).config(dispatch.Config{Limit: 40, Base: "batch", Concurrency: 4}).sized(80, 40)
	now := time.Now()
	for i, id := range []string{"orchestra-evsp", "orchestra-74b2.18", "orchestra-6i2h", "orchestra-a1b"} {
		f.running(dispatch.Status{Ticket: id, Title: "Ticket " + id, Agent: "working",
			Started: now.Add(-time.Duration(4-i) * time.Minute)})
	}
	return f.keys("s", "y").dash()
}

func receive(m Dashboard, msgs ...tea.Msg) Dashboard {
	for _, msg := range msgs {
		next, _ := m.Update(msg)
		m = next.(Dashboard)
	}
	return m
}

// The line names no ticket and never wraps: a narrower pane drops words, the count, then all but the
// squares.
func TestWindDownLineDropsWordsRatherThanWrap(t *testing.T) {
	m := windingDown(t)
	for _, c := range []struct {
		w    int
		want string
	}{
		{80, " ■ ■ ■ ■  Winding down · 4 running, no new tickets"},
		{50, " ■ ■ ■ ■  Winding down · 4 running, no new tickets"},
		{49, " ■ ■ ■ ■  Winding down · 4 running"},
		{34, " ■ ■ ■ ■  Winding down · 4 running"},
		{33, " ■ ■ ■ ■  Winding down"},
		{22, " ■ ■ ■ ■  Winding down"},
		{21, " ■ ■ ■ ■"},
		{5, " ■ ■"},
	} {
		line := m.windDownLine(c.w)
		if got := ansi.Strip(line); got != c.want {
			t.Errorf("%d columns: %q, want %q", c.w, got, c.want)
		}
		if strings.Contains(line, "\n") || ansi.StringWidth(line) > c.w {
			t.Errorf("%d columns: the line wraps or overflows: %q", c.w, ansi.Strip(line))
		}
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, " ■ ■ ■ ■  Winding down · 4 running, no new tickets") ||
		strings.Contains(v, "orchestra-evsp, ") {
		t.Errorf("want the line, with no list of the running tickets:\n%s", v)
	}
}

// The squares fill up as the tickets running at the start finish: green once merged, red once set
// aside, amber while they run; the words are amber, never red, as is the queue's held marker.
func TestWindDownLineFillsUpAsTicketsFinish(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	lipgloss.SetHasDarkBackground(true)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	m := windingDown(t)
	squares := func(m Dashboard) string { return strings.Join(strings.Fields(m.windDownLine(80))[:4], " ") }
	if got, want := squares(m), strings.Repeat(amberSquare+" ", 3)+amberSquare; got != want {
		t.Errorf("all running: %q, want %q", got, want)
	}
	line := m.windDownLine(80)
	if !strings.Contains(line, "\x1b[1;38;2;251;146;60mWinding down\x1b[0m") || strings.Contains(line, "248;113;113") {
		t.Errorf("want Winding down in bold amber and nothing red: %q", line)
	}
	if held := "\x1b[38;2;251;146;60m · held\x1b[0m"; !strings.Contains(m.View(), held) {
		t.Errorf("the totals lack the amber %q:\n%q", held, m.View())
	}

	m = receive(m,
		statusMsg(dispatch.Status{Ticket: "orchestra-evsp", Gone: true}),
		eventMsg(dispatch.Event{Kind: dispatch.EvClosed, Ticket: "orchestra-evsp", Detail: "abc1234 merged"}),
		statusMsg(dispatch.Status{Ticket: "orchestra-6i2h", Gone: true}),
		eventMsg(dispatch.Event{Kind: dispatch.EvDeferred, Ticket: "orchestra-6i2h", Detail: "checks failed"}))
	if got, want := squares(m), greenSquare+" "+amberSquare+" "+redSquare+" "+amberSquare; got != want {
		t.Errorf("one merged, one set aside: %q, want %q", got, want)
	}
	if got := ansi.Strip(m.windDownLine(80)); got != " ■ ■ ■ ■  Winding down · 2 running, no new tickets" {
		t.Errorf("one merged, one set aside: %q", got)
	}

	m = receive(m,
		statusMsg(dispatch.Status{Ticket: "orchestra-74b2.18", Gone: true}),
		statusMsg(dispatch.Status{Ticket: "orchestra-a1b", Gone: true}),
		eventMsg(dispatch.Event{Kind: dispatch.EvClosed, Ticket: "orchestra-74b2.18", Detail: "def5678 merged"}))
	// orchestra-a1b has left Current but neither merged nor been set aside yet: it stays amber.
	if got, want := squares(m), greenSquare+" "+greenSquare+" "+redSquare+" "+amberSquare; got != want {
		t.Errorf("all finished: %q, want %q", got, want)
	}
	if got := ansi.Strip(m.windDownLine(80)); got != " ■ ■ ■ ■  Winding down · nothing running, ending now" {
		t.Errorf("all finished: %q", got)
	}
}

// Taken back and asked again, the wind-down counts the tickets running then, not those of the first
// time; asked with nothing running, the line has one square and says the run ends.
func TestWindDownLineStartsAgainWhenAskedAgain(t *testing.T) {
	m := windingDown(t)
	m = receive(m,
		statusMsg(dispatch.Status{Ticket: "orchestra-evsp", Gone: true}),
		eventMsg(dispatch.Event{Kind: dispatch.EvClosed, Ticket: "orchestra-evsp", Detail: "abc1234 merged"}),
		eventMsg(dispatch.Event{Kind: dispatch.EvResume, Text: "DRAIN cancelled"}),
		eventMsg(dispatch.Event{Kind: dispatch.EvDrain, Text: "DRAIN: …, asked by SIGUSR1"}))
	if got := ansi.Strip(m.windDownLine(80)); got != " ■ ■ ■  Winding down · 3 running, no new tickets" {
		t.Errorf("asked again with three running: %q", got)
	}

	m = receive(run(t).sized(80, 40).dash(), eventMsg(dispatch.Event{Kind: dispatch.EvDrain, Text: "DRAIN: stopping now, as nothing is running"}))
	if got := ansi.Strip(m.windDownLine(80)); got != " ■  Winding down · nothing running, ending now" {
		t.Errorf("asked with nothing running: %q", got)
	}
}
