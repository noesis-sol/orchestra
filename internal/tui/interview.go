package tui

import (
	"fmt"
	"io"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// While a typed feature is talked through in a Herdr pane beside orchestra's, orchestra's pane says
// where the interview stands on one line under its fixed message, updated in place: claude's state
// and how long the interview has run, or, once claude has filed the feature, the epic.

// InterviewProgress is where a feature interview in a pane stands.
type InterviewProgress struct {
	Agent   dispatch.AgentState // claude's state as Herdr last read it; "" if Herdr hasn't said yet
	Elapsed time.Duration       // since the interview began
	Epic    string              // the epic claude filed, once feature.json names it
	Title   string              // the epic's title, once bd has given it and its tickets
	Tickets int                 // how many tickets the epic has, when Title is set
}

// Line is the progress as one line, styled as the dashboard's are and at most width columns wide
// (20 at the least): the epic once it is filed, else claude's state in words and the time.
func (p InterviewProgress) Line(width int) string {
	width = max(width, 20)
	if p.Epic != "" {
		head, tail := "Filed "+p.Epic, ""
		if p.Title != "" {
			tail = " (" + plural(p.Tickets, "ticket", "tickets") + ")"
		}
		// The title gives way first, so that a narrow pane still shows how many tickets there are.
		room := width - ansi.StringWidth(head+": "+tail)
		line := closedStyle.Render(head)
		if p.Title != "" && room > 0 {
			line += ": " + ansi.Truncate(oneLine(p.Title), room, "…") + dimStyle.Render(tail)
		}
		return ansi.Truncate(line, width, "…")
	}
	return ansi.Truncate(interviewState(p.Agent)+"  "+dimStyle.Render(p.Elapsed.Truncate(time.Second).String()),
		width, "…")
}

// interviewState is claude's state in words, coloured as the dashboard colours a worker's.
func interviewState(st dispatch.AgentState) string {
	switch st {
	case dispatch.StateWorking:
		return pickedStyle.Render("claude is working")
	case dispatch.StateBlocked: // a permission prompt, or the dialog asking whether to trust the folder
		return stopStyle.Render("claude is waiting for a permission answer")
	case dispatch.StateIdle, dispatch.StateDone: // done is the end of a turn, until the pane is looked at
		return deferredStyle.Render("claude is waiting for you")
	}
	return "claude is running" // Herdr can't tell what claude does, or hasn't said yet
}

// InterviewLine draws an interview's progress on a terminal, Out, as one line at the cursor that
// each Show replaces: the cursor stays on it, at no fixed column. Width reads the terminal's width.
type InterviewLine struct {
	Out   io.Writer
	Width func() int
	drawn bool
}

// Show replaces the line with p. It stays off the terminal's last column: a terminal that wraps a
// line as soon as it is filled would take the cursor to the next, where \r can't reach the line.
func (l *InterviewLine) Show(p InterviewProgress) {
	fmt.Fprint(l.Out, "\r"+ansi.EraseLineRight+p.Line(l.Width()-1))
	l.drawn = true
}

// Clear removes the line, if one is drawn, and leaves the cursor at the start of the empty line,
// where the cursor was before the first Show.
func (l *InterviewLine) Clear() {
	if l.drawn {
		fmt.Fprint(l.Out, "\r"+ansi.EraseLineRight)
		l.drawn = false
	}
}
