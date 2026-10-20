package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// windDownLine says, while the run winds down, how far it has come: a ■ per ticket running when it
// began, amber while it runs, green once merged or red once set aside or waiting on the maintainer,
// then how many still run. It names no tickets: Current and the tickets table do. It never wraps: a
// narrow pane drops the words, then all but the squares.
func (m Dashboard) windDownLine(w int) string {
	squares := make([]string, 0, max(len(m.drainFrom), 1))
	for _, id := range m.drainFrom {
		squares = append(squares, m.windDownSquare(id))
	}
	if len(squares) == 0 { // nothing was running: the run ends at once
		squares = append(squares, windDownStyle.Render("■"))
	}
	lead := " " + strings.Join(squares, " ") + "  "
	words := windDownStyle.Bold(true).Render("Winding down")
	rests := []string{"nothing running, ending now"}
	if n := len(m.Running()); n > 0 {
		rests = []string{fmt.Sprintf("%d running, no new tickets", n), fmt.Sprintf("%d running", n)}
	}
	for _, rest := range rests {
		if line := lead + words + dimStyle.Render(" · "+rest); ansi.StringWidth(line) <= w {
			return line
		}
	}
	if line := lead + words; ansi.StringWidth(line) <= w {
		return line
	}
	return " " + strings.Join(squares[:min(w/2, len(squares))], " ") // as many squares as fit
}

// windDownSquare is ticket id's ■ on the winding-down line, in the colour of where it is: amber while
// it runs, then its row state's (rowLooks).
func (m Dashboard) windDownSquare(id string) string {
	style := windDownStyle // still running, or gone from Current with no row
	if i := m.find(id); i >= 0 && m.records[i].section() == inTable {
		style = rowLooks[m.records[i].row.state].square
	}
	return style.Render("■")
}

// runningIDs names the running tickets, oldest first.
func (m Dashboard) runningIDs() []string {
	var ids []string
	for _, st := range m.Running() {
		ids = append(ids, st.Ticket)
	}
	return ids
}

// drainQuestion is the question s asks, worded once for the box and the one-line prompt: whether
// to stop after the running tickets, or, while the run winds down, whether to take tickets again.
type drainQuestion struct {
	ask   string    // the question
	short string    // the question where the prompt line has no room for ask
	about string    // what y does, in the DRAIN line's words
	keys  []keyHint // y and n with what each does, which the box names short
}

// yesNo is the stop question's keys: y, then n, with what each does.
func yesNo(y, n string) []keyHint {
	return []keyHint{{key: "y", short: y}, {key: "n", short: n}}
}

func (m Dashboard) drainQuestion() drainQuestion {
	if m.draining {
		return drainQuestion{ask: "Keep taking tickets?", short: "Keep taking tickets?",
			about: "New tickets start again as slots free up.", keys: yesNo("keep going", "keep stopping")}
	}
	q := drainQuestion{ask: "Stop after the running tickets?", short: "Stop after current?",
		keys: yesNo("stop after current", "keep going")}
	ids := m.runningIDs()
	for i, id := range ids {
		ids[i] = oneLine(id)
	}
	lead, list, tail := dispatch.DrainWords(ids)
	switch len(ids) {
	case 0:
		q.about = "No new tickets will start. Nothing is running, so the run ends now."
		q.keys = yesNo("stop", "keep going")
	case 1:
		q.about = capitalize(lead+list+tail) + ". It merges as usual, then the run ends."
	default:
		q.about = capitalize(lead+list+tail) + ". They merge as usual, then the run ends."
	}
	return q
}

// The drain question's box: the narrowest pane it is drawn over, the widest it gets, and how many
// lines of the dashboard it leaves showing, above and below it, or it goes on a line of its own.
const (
	modalMinWidth = 36
	modalMaxWidth = 64
	modalRoom     = 2
)

// modal is the drain question in a box, its keys named as the hint names its own. It is no wider than
// what a box as wide as the pane holds, so the dashboard's borders show either side.
func (m Dashboard) modal(w int) string {
	q := m.drainQuestion()
	body := lipgloss.JoinVertical(lipgloss.Left,
		deferredStyle.Bold(true).Render(q.ask), "", q.about, "", keyWords(q.keys, true))
	return box(min(boxInner(w), modalMaxWidth), 0, yellow, body)
}

// promptLine is the drain question on one line, for a pane too small for the box; the question and
// keys come first so a narrow pane keeps them. y and n are in keyStyle, as everywhere else.
func (m Dashboard) promptLine(w int) string {
	q := m.drainQuestion()
	ask := q.ask
	if ansi.StringWidth(" "+ask+" y/n") > w {
		ask = q.short
	}
	asked := deferredStyle.Bold(true)
	line := asked.Render(" "+ask+" ") + keyStyle.Render("y") + dimStyle.Render("/") + keyStyle.Render("n") +
		asked.Render(" · "+q.about)
	return ansi.Truncate(line, w, "…")
}
