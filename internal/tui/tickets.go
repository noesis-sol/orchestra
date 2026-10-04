package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/x/ansi"
)

// rowState starts at working on purpose: a row is added when its ticket is picked up, so an unset
// state means the worker is still at it.
type rowState int

const (
	rowWorking rowState = iota
	rowDone
	rowDeferred
	rowReview
	rowStopped
	rowAsked
)

type ticketRow struct {
	id, title string
	state     rowState
	note      string // the merged commit, or why it was set aside
	triage    string // the triage organ's verdict
}

func (m *Dashboard) rowIndex(id string) int {
	for i := len(m.rows) - 1; i >= 0; i-- {
		if m.rows[i].id == id {
			return i
		}
	}
	return -1
}

// setRow sets ticket id's row to state, with note. A row that leaves rowAsked (its worker deferred
// the ticket in its tab, say) no longer needs the maintainer.
func (m *Dashboard) setRow(id string, state rowState, note string) {
	i := m.rowIndex(id)
	if i < 0 {
		m.rows = append(m.rows, ticketRow{id: id})
		i = len(m.rows) - 1
	}
	if m.rows[i].state == rowAsked && state != rowAsked {
		m.asked--
	}
	m.rows[i].state, m.rows[i].note = state, note
}

// working shows ticket id as picked up. A ticket back from a question, or deferred earlier in the
// run, keeps its row, which no longer needs the maintainer and drops the old verdict; title ""
// keeps the row's.
func (m *Dashboard) working(id, title string) {
	i := m.rowIndex(id)
	if i < 0 {
		m.rows = append(m.rows, ticketRow{id: id, title: title, state: rowWorking})
		return
	}
	if m.rows[i].state == rowAsked {
		m.asked--
	}
	m.rows[i].state, m.rows[i].note, m.rows[i].triage = rowWorking, "", ""
	if title != "" {
		m.rows[i].title = title
	}
}

// cells renders one row: state, ticket ID, and what to say about it. A picked-up ticket shows
// its title, and in its state what its worker is doing (doingLabel, or ""); a completed one only
// its merged commit; a set-aside one why, with triage's verdict.
func (r ticketRow) cells(width int, doing string) [3]string {
	fit := func(s string) string { return ansi.Truncate(oneLine(s), max(width, 8), "…") }
	switch r.state {
	case rowDone:
		return [3]string{closedStyle.Render("✓ done"), closedStyle.Render(r.id), dimStyle.Render(fit(r.note))}
	case rowDeferred, rowReview:
		label := "↷ deferred"
		if r.state == rowReview {
			label = "! review"
		}
		about := dimStyle.Render(fit(r.note))
		if r.triage != "" {
			about = organStyle.Render(fit("◆ " + r.triage))
		}
		return [3]string{deferredStyle.Render(label), deferredStyle.Render(r.id), about}
	case rowStopped:
		return [3]string{stopStyle.Render("■ stopped"), stopStyle.Render(r.id), fit(r.title)}
	case rowAsked:
		return [3]string{stopStyle.Render("? for you"), stopStyle.Render(r.id), fit(r.note)}
	}
	state := pickedStyle.Render("▶ working")
	switch doing {
	case "resolving":
		state = deferredStyle.Render("⟳ resolving")
	case "testing":
		state = testingStyle.Render("▶ testing")
	case "editing", "reading":
		state = pickedStyle.Render("▶ " + doing)
	}
	return [3]string{state, pickedStyle.Render(r.id), fit(r.title)}
}

// ticketsTable lists this run's tickets, newest last, in at most maxLines lines of screen.
func (m Dashboard) ticketsTable(w, maxLines int) string {
	if len(m.rows) == 0 {
		return ""
	}
	idWidth := 7
	for _, r := range m.rows {
		idWidth = max(idWidth, len(r.id))
	}
	aboutWidth := w - 2 - 13 - (idWidth + 2) - 2 - 2 // borders, state and ID columns, separators, padding
	rows := m.rows
	hidden := 0
	if limit := max(maxLines-4, 3); len(rows) > limit { // header, rule and borders take 4 lines
		hidden = len(rows) - (limit - 1)
		rows = rows[hidden:]
	}
	var data [][]string
	if hidden > 0 {
		more := ansi.Truncate("earlier tickets, see the log", max(aboutWidth, 8), "…")
		data = append(data, []string{"", dimStyle.Render(fmt.Sprintf("+%d", hidden)), dimStyle.Render(more)})
	}
	for _, r := range rows {
		doing := ""
		if st, ok := m.active[r.id]; ok {
			doing = doingLabel(st)
		}
		c := r.cells(aboutWidth, doing)
		data = append(data, c[:])
	}
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(grey)).
		Headers("Tickets", "", "").
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			switch {
			case row == table.HeaderRow:
				return s.Faint(true)
			case col == 0:
				return s.Width(13)
			case col == 1:
				return s.Width(idWidth + 2)
			}
			return s
		}).
		Rows(data...).
		Width(w).
		Render()
}

// statsTable is the run's totals as one strip: a column per total, label over value, in the
// events' colours. Too narrow a pane gets the one-line form instead.
func (m Dashboard) statsTable(w int) string {
	count := func(n int, mark string, style lipgloss.Style) string {
		if n == 0 {
			return dimStyle.Render("0")
		}
		return style.Render(fmt.Sprintf("%s %d", mark, n))
	}
	deferred := count(m.deferred, "↷", deferredStyle)
	if m.triaged > 0 {
		deferred += organStyle.Render(fmt.Sprintf(" ◆ %d", m.triaged))
	}
	queued := dimStyle.Render("—")
	if m.queued >= 0 {
		queued = fmt.Sprint(m.queued)
	}
	if m.draining {
		queued += deferredStyle.Render(" · held") // not taken while the run winds down
	}
	labels := []string{"Completed", "Deferred", "Needs you", "Workers", "In queue"}
	values := []string{
		count(m.closed, "✓", closedStyle),
		deferred,
		count(m.asked, "?", stopStyle),
		pickedStyle.Render(fmt.Sprint(len(m.active))) + dimStyle.Render(fmt.Sprintf(" of %d", max(m.cfg.Concurrency, 1))),
		queued,
	}
	need := len(labels) + 1 // borders
	for i := range labels {
		need += max(ansi.StringWidth(labels[i]), ansi.StringWidth(values[i])) + 2
	}
	if w < need {
		return m.statsLine(w)
	}
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(grey)).
		BorderHeader(false).
		StyleFunc(func(row, _ int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return s.Faint(true)
			}
			return s
		}).
		Headers(labels...).
		Rows(values).
		Width(w).
		Render()
}

// statsLine is the totals on one line, for a pane too narrow or too short for the strip. The
// branch, running time and stopping state are on the title line, the winding down above the hint.
func (m Dashboard) statsLine(w int) string {
	queued := "—"
	if m.queued >= 0 {
		queued = fmt.Sprint(m.queued)
	}
	if m.draining {
		queued += " held" // not taken while the run winds down
	}
	parts := []string{
		closedStyle.Render(fmt.Sprintf("✓ %d", m.closed)),
		deferredStyle.Render(fmt.Sprintf("↷ %d", m.deferred)),
		stopStyle.Render(fmt.Sprintf("? %d", m.asked)),
		pickedStyle.Render(fmt.Sprintf("▶ %d", len(m.active))) +
			dimStyle.Render(fmt.Sprintf("/%d", max(m.cfg.Concurrency, 1))),
		dimStyle.Render("queue " + queued),
	}
	return ansi.Truncate(" "+strings.Join(parts, dimStyle.Render(" · ")), w, "…")
}
