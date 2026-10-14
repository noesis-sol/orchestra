package tui

import (
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
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
	rowBlocked // its work is done, but it can't merge until the maintainer acts
	rowAsked
	rowWaiting // started before a ticket blocking it merged: back in the queue until it does
)

// rowLook is how the dashboard shows a row state, and what the row counts as.
type rowLook struct {
	glyph, word string         // its state in the tickets table, as in "✓ done"; the glyph heads its events' lines
	style       lipgloss.Style // the state's and the ID's colour, and its events'
	title       bool           // its last column has the ticket's title, not the row's note
	dim         bool           // its last column is faint
	triage      bool           // triage's verdict, once there is one, replaces the note
	tally       tally          // the total it counts under, if any
	square      lipgloss.Style // its ■ on the winding-down line, once its ticket has left Current
	live        bool           // its worker is still at it: its state is its worker's, and a stop stops it
}

// tally is a total of the run that counts rows: those set aside, and those waiting on the maintainer.
type tally int

const (
	tallyNone tally = iota
	tallySetAside
	tallyNeedsYou
)

// setAsideSquare is the winding-down line's ■ for a ticket set aside, stopped or waiting on the maintainer.
var setAsideSquare = stopStyle.UnsetBold()

// rowLooks are the row states' looks. A row state's events, if it has any, are in rowEvents.
var rowLooks = [...]rowLook{
	// Not merged or set aside yet: amber on the winding-down line, as while it runs.
	rowWorking: {glyph: "▶", word: "working", style: pickedStyle, title: true, square: windDownStyle, live: true},
	rowDone:    {glyph: "✓", word: "done", style: closedStyle, dim: true, square: doneStyle},
	rowDeferred: {glyph: "↷", word: "deferred", style: deferredStyle, dim: true, triage: true,
		tally: tallySetAside, square: setAsideSquare},
	rowReview: {glyph: "!", word: "review", style: deferredStyle, dim: true, triage: true,
		tally: tallySetAside, square: setAsideSquare},
	rowStopped: {glyph: "■", word: "stopped", style: stopStyle, title: true, square: setAsideSquare},
	// Red and "blocked", as a worker's box says of an agent waiting for the maintainer.
	rowBlocked: {glyph: "■", word: "blocked", style: stopStyle, tally: tallyNeedsYou, square: setAsideSquare},
	rowAsked:   {glyph: "?", word: "for you", style: stopStyle, tally: tallyNeedsYou, square: setAsideSquare},
	// Back in the queue, which the wind-down holds.
	rowWaiting: {glyph: "◷", word: "waiting", style: dimStyle, dim: true, square: dimStyle},
}

// label is the state as the tickets table shows it.
func (l rowLook) label() string { return l.style.Render(l.glyph + " " + l.word) }

type ticketRow struct {
	id, title string
	state     rowState
	note      string // the merged commit, or why it was set aside
	triage    string // the triage organ's verdict
}

func (m *Dashboard) rowIndex(id string) int {
	for i, r := range slices.Backward(m.rows) {
		if r.id == id {
			return i
		}
	}
	return -1
}

// setRow sets ticket id's row to state, with note.
func (m *Dashboard) setRow(id string, state rowState, note string) {
	i := m.rowIndex(id)
	if i < 0 {
		m.rows = append(m.rows, ticketRow{id: id})
		i = len(m.rows) - 1
	}
	m.rows[i].state, m.rows[i].note = state, note
}

// markBlocked shows ev's ticket as blocked, its work done but its merge waiting on the maintainer, with
// why, and reports whether ev says so (Event.Blocked).
func (m *Dashboard) markBlocked(ev dispatch.Event) bool {
	if ev.Ticket == "" || ev.Blocked == "" {
		return false
	}
	m.setRow(ev.Ticket, rowBlocked, ev.Blocked)
	return true
}

// needsYou is how many tickets wait on the maintainer: a question to answer, or a finished ticket
// to merge. A row that leaves either state (its worker deferred the ticket in its tab, say) no
// longer counts.
func (m Dashboard) needsYou() int { return m.count(tallyNeedsYou) }

// setAside is how many tickets the run has set aside: deferred, or left for review. One that
// leaves either state (back in the queue after an environment hold, merged after a check run
// again, dispatched again) no longer counts. One blocked from merging counts under needsYou.
func (m Dashboard) setAside() int { return m.count(tallySetAside) }

// count is how many rows count under t.
func (m Dashboard) count(t tally) int {
	n := 0
	for _, r := range m.rows {
		if rowLooks[r.state].tally == t {
			n++
		}
	}
	return n
}

// pickUp shows ticket id as picked up. A ticket back from a question, or deferred earlier in the
// run, keeps its row, which drops the old verdict; title "" keeps the row's.
func (m *Dashboard) pickUp(id, title string) {
	i := m.rowIndex(id)
	if i < 0 {
		m.rows = append(m.rows, ticketRow{id: id, title: title, state: rowWorking})
		return
	}
	m.rows[i].state, m.rows[i].note, m.rows[i].triage = rowWorking, "", ""
	if title != "" {
		m.rows[i].title = title
	}
}

// cells renders one row: state, ticket ID, and what to say about it, as its state's look has it. A
// picked-up ticket shows its title, and in its state what its worker is doing (worker, its look, or
// nil), or once it is done, where its work is in the merge queue; a completed one only its merged
// commit; a set-aside one why, with triage's verdict; a blocked one why it can't merge.
func (r ticketRow) cells(width int, worker *look) [3]string {
	fit := func(s string) string { return ansi.Truncate(oneLine(s), max(width, minAboutWidth), "…") }
	l := rowLooks[r.state]
	state := l.label()
	if l.live && worker != nil && worker.row.text != "" {
		state = worker.row.render()
	}
	about := r.note
	if l.title {
		about = r.title
	}
	about = fit(about)
	switch {
	case l.triage && r.triage != "":
		about = organStyle.Render(fit("◆ " + r.triage))
	case l.dim:
		about = dimStyle.Render(about)
	}
	return [3]string{state, l.style.Render(r.id), about}
}

// tickets is the tickets table headed by its Tickets label, lines lines in all.
func (m Dashboard) tickets(w, lines int) string {
	return stack(sectionLabel("Tickets", ticketsStyle), m.ticketsTable(w, lines-labelLines))
}

// The tickets table's columns: the state column's width, the narrowest its ID column and the text in
// its last column get, and the padding each column has, a space either side.
const (
	stateColWidth = 13
	minIDWidth    = 7
	minAboutWidth = 8
	cellPadding   = 2
)

// aboutWidth is the room for the text in the last column of a tickets table w wide whose IDs take
// idWidth: what the table's four vertical lines (its borders and the two between its columns), the
// state and ID columns and the last column's padding leave.
func aboutWidth(w, idWidth int) int {
	return w - 4 - stateColWidth - (idWidth + cellPadding) - cellPadding
}

// ticketsTable lists this run's tickets, newest last, in lines lines of screen: blank rows fill what
// the tickets leave, and when there are more the oldest give way to a count of them.
func (m Dashboard) ticketsTable(w, lines int) string {
	idWidth := minIDWidth
	for _, r := range m.rows {
		idWidth = max(idWidth, ansi.StringWidth(r.id))
	}
	// IDs longer than a third of the table are cut, so the titles keep their room
	idWidth = min(idWidth, max(w/3, minIDWidth))
	about := aboutWidth(w, idWidth)
	rows := m.rows
	hidden := 0
	limit := max(lines-borderLines, 1)
	if len(rows) > limit {
		hidden = len(rows) - (limit - 1)
		rows = rows[hidden:]
	}
	var data [][]string
	if hidden > 0 {
		more := ansi.Truncate("earlier tickets, see the log", max(about, minAboutWidth), "…")
		data = append(data, []string{"", dimStyle.Render(fmt.Sprintf("+%d", hidden)), dimStyle.Render(more)})
	}
	for _, r := range rows {
		var worker *look
		if st, ok := m.active[r.id]; ok {
			l := workerLook(st)
			worker = &l
		}
		r.id = ansi.Truncate(r.id, idWidth, "…")
		c := r.cells(about, worker)
		data = append(data, c[:])
	}
	for len(data) < limit {
		data = append(data, []string{"", "", ""})
	}
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(grey)).
		StyleFunc(func(_, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			switch col {
			case 0:
				return s.Width(stateColWidth)
			case 1:
				return s.Width(idWidth + cellPadding)
			}
			return s
		}).
		Rows(data...).
		Width(w).
		Render()
}

// total is one of the run's counted totals, which the strip and the one-line form show.
type total struct {
	label string         // its heading in the strip
	mark  string         // what goes before its count
	n     int            // the count
	style lipgloss.Style // the count's colour
	of    int            // what it counts out of (the workers' slots), or 0 for nothing
	extra string         // what follows it in the strip, styled: triage's verdicts
}

// totals are the run's counted totals: the tickets completed, set aside and waiting on the
// maintainer, and the running workers.
func (m Dashboard) totals() []total {
	var verdicts string
	if m.triaged > 0 {
		verdicts = organStyle.Render(fmt.Sprintf(" ◆ %d", m.triaged))
	}
	return []total{
		{label: "Completed", mark: "✓", n: m.closed, style: closedStyle},
		{label: "Set aside", mark: "↷", n: m.setAside(), style: deferredStyle, extra: verdicts},
		{label: "Needs you", mark: "?", n: m.needsYou(), style: stopStyle},
		{label: "Workers", mark: "▶", n: len(m.active), style: pickedStyle, of: m.slots()},
	}
}

// cell is t in the strip: a count out of something in full, a zero faint, the others with their mark.
func (t total) cell() string {
	switch {
	case t.of > 0:
		return t.style.Render(fmt.Sprint(t.n)) + dimStyle.Render(fmt.Sprintf(" of %d", t.of))
	case t.n == 0:
		return dimStyle.Render("0") + t.extra
	}
	return t.style.Render(fmt.Sprintf("%s %d", t.mark, t.n)) + t.extra
}

// part is t on the one-line form: its mark and count, and what it counts out of.
func (t total) part() string {
	p := t.style.Render(fmt.Sprintf("%s %d", t.mark, t.n))
	if t.of > 0 {
		p += dimStyle.Render(fmt.Sprintf("/%d", t.of))
	}
	return p
}

// queueCount is the queue's total: how many ready tickets wait for a slot, "—" until the loop has
// said, and what follows that: " of " the tickets not closed, once the loop has said how many.
func (m Dashboard) queueCount() (n, of string) {
	if m.queued < 0 {
		return "—", ""
	}
	if m.unclosed >= 0 {
		of = fmt.Sprintf(" of %d", m.unclosed)
	}
	return fmt.Sprint(m.queued), of
}

// heldBack is what follows the queue's count while ready tickets are held back, waiting for
// something besides a slot (an overlap with a running ticket, their subtickets, a ticket to merge, a
// solo ticket): " · 4 held back", or "" with none.
func (m Dashboard) heldBack() string {
	if n := m.held.Total(); n > 0 && m.queued >= 0 {
		return heldBackStyle.Render(fmt.Sprintf(" · %d held back", n))
	}
	return ""
}

// statsTable is the run's totals as one strip: a column per total, label over value, in the
// events' colours. Too narrow a pane gets the one-line form instead.
func (m Dashboard) statsTable(w int) string {
	var labels, values []string
	for _, t := range m.totals() {
		labels, values = append(labels, t.label), append(values, t.cell())
	}
	n, of := m.queueCount()
	queued := n + dimStyle.Render(of)
	if m.queued < 0 {
		queued = dimStyle.Render(n)
	}
	queued += m.heldBack()
	if m.draining {
		queued += windDownStyle.Render(" · held") // not taken while the run winds down
	}
	labels, values = append(labels, "In queue"), append(values, queued)
	need := len(labels) + 1 // borders
	for i := range labels {
		need += max(ansi.StringWidth(labels[i]), ansi.StringWidth(values[i])) + cellPadding
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
	var parts []string
	for _, t := range m.totals() {
		parts = append(parts, t.part())
	}
	n, of := m.queueCount()
	queue := dimStyle.Render("queue "+n+of) + m.heldBack()
	if m.draining {
		queue += windDownStyle.Render(" held") // not taken while the run winds down
	}
	parts = append(parts, queue)
	return ansi.Truncate(" "+strings.Join(parts, dimStyle.Render(" · ")), w, "…")
}
