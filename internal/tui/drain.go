package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// windDownLine says, while the run winds down, after which tickets it ends, in the DRAIN line's
// words and as tickets finish. A narrow pane wraps it rather than lose words: only the ticket IDs
// are shortened.
func (m Dashboard) windDownLine(w int) string {
	lead, list, tail := dispatch.DrainWords(m.runningIDs())
	lines := wrapAround(" ■ "+capitalize(lead), list, tail, w, "   ")
	for i := range lines {
		lines[i] = stopStyle.Render(lines[i])
	}
	return strings.Join(lines, "\n")
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
	lead, list, tail := dispatch.DrainWords(m.runningIDs())
	switch len(m.active) {
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

// modal is the drain question in a box, its keys named as the hint names its own.
func (m Dashboard) modal(w int) string {
	q := m.drainQuestion()
	body := lipgloss.JoinVertical(lipgloss.Left,
		deferredStyle.Bold(true).Render(q.ask), "", q.about, "", keyWords(q.keys, true))
	return box(min(w-4, 64), 0, yellow, body)
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
