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

// record is all the dashboard knows of one ticket of the run: its row in the tickets table, once an
// event has given it one, and its worker's status while it runs. Each ticket has one record, so what
// Current and the tickets table show of it can't disagree on whether it runs.
type record struct {
	row     ticketRow
	listed  bool            // it has a row: an event picked it up or set it; a status alone gives it none
	running bool            // its worker runs: status is its latest, until a Gone one
	status  dispatch.Status // its worker's latest status, while running
}

// section is where a frame shows a ticket: in Current, in the tickets table, or, a worker's status
// alone with no event about its ticket yet, nowhere.
type section int

const (
	nowhere section = iota
	inCurrent
	inTable
)

// section is where a frame that draws Current shows r's ticket: in Current while its worker runs,
// else in the tickets table once it has a row; never both. A ticket's end event (closed, deferred,
// blocked) comes before the status that takes it out of Current, so its row, no longer live, comes in
// only as it leaves. A frame without Current (the summary, a pane too small for both) shows every row
// in the table.
func (r record) section() section {
	switch {
	case r.running:
		return inCurrent
	case r.listed:
		return inTable
	}
	return nowhere
}

// find is the index of ticket id's record, or -1.
func (m Dashboard) find(id string) int {
	return slices.IndexFunc(m.records, func(r record) bool { return r.row.id == id })
}

// hasRow reports whether ticket id has a row in the tickets table.
func (m Dashboard) hasRow(id string) bool {
	i := m.find(id)
	return i >= 0 && m.records[i].listed
}

// rows are the tickets table's rows, every ticket that has one, in their order.
func (m Dashboard) rows() []ticketRow {
	var rows []ticketRow
	for _, r := range m.listedRecords() {
		rows = append(rows, r.row)
	}
	return rows
}

// listedRecords are the records of the tickets that have a row, in the rows' order.
func (m Dashboard) listedRecords() []record {
	return slices.DeleteFunc(slices.Clone(m.records), func(r record) bool { return !r.listed })
}

// listRow gives ticket id a row, if it has none, to be set to state to, and returns its record's
// index. A new row comes last, as does one whose worker is no longer at it as it is set to to: the
// rows are in the order their tickets came to an end, so that one leaving Current comes in under the
// others in the tickets table beside it.
func (m *Dashboard) listRow(id string, to rowState) int {
	i := m.find(id)
	if i < 0 {
		m.records = append(m.records, record{row: ticketRow{id: id}})
		i = len(m.records) - 1
	}
	if r := m.records[i]; !r.listed || rowLooks[r.row.state].live && !rowLooks[to].live {
		m.records = append(slices.Delete(m.records, i, i+1), r)
		i = len(m.records) - 1
	}
	m.records[i].listed = true
	return i
}

// setRow sets ticket id's row to state, with note. A row whose worker is no longer at it moves to the
// end (see listRow).
func (m *Dashboard) setRow(id string, state rowState, note string) {
	i := m.listRow(id, state)
	m.records[i].row.state, m.records[i].row.note = state, note
}

// rowsBesideCurrent are the records of the rows the tickets table has when Current is drawn with it:
// those of the tickets the frame shows in the table (see record.section).
func (m Dashboard) rowsBesideCurrent() []record {
	return slices.DeleteFunc(slices.Clone(m.records), func(r record) bool { return r.section() != inTable })
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
	for _, r := range m.rows() {
		if rowLooks[r.state].tally == t {
			n++
		}
	}
	return n
}

// pickUp shows ticket id as picked up. A ticket back from a question, or deferred earlier in the
// run, keeps its row, which drops the old verdict; title "" keeps the row's.
func (m *Dashboard) pickUp(id, title string) {
	r := &m.records[m.listRow(id, rowWorking)]
	r.row.state, r.row.note, r.row.triage = rowWorking, "", ""
	if title != "" {
		r.row.title = title
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

// tickets is the tickets table headed by its Tickets label, lines lines in all, with every row: drawn
// where Current isn't, so that a running ticket is always shown somewhere.
func (m Dashboard) tickets(w, lines int) string {
	return stack(sectionLabel("Tickets", ticketsStyle), m.ticketsTable(w, lines-labelLines))
}

// ticketsBesideCurrent is tickets drawn with Current: without the rows of the tickets Current shows.
func (m Dashboard) ticketsBesideCurrent(w, lines int) string {
	return stack(sectionLabel("Tickets", ticketsStyle), m.rowsTable(w, lines-labelLines, m.rowsBesideCurrent()))
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
func (m Dashboard) ticketsTable(w, lines int) string { return m.rowsTable(w, lines, m.listedRecords()) }

// rowsTable is ticketsTable listing the rows of recs.
func (m Dashboard) rowsTable(w, lines int, recs []record) string {
	idWidth := minIDWidth
	for _, r := range recs {
		idWidth = max(idWidth, ansi.StringWidth(oneLine(r.row.id)))
	}
	// IDs longer than a third of the table are cut, so the titles keep their room
	idWidth = min(idWidth, max(w/3, minIDWidth))
	about := aboutWidth(w, idWidth)
	hidden := 0
	limit := max(lines-borderLines, 1)
	if len(recs) > limit {
		hidden = len(recs) - (limit - 1)
		recs = recs[hidden:]
	}
	var data [][]string
	if hidden > 0 {
		more := ansi.Truncate("earlier tickets, see the log", max(about, minAboutWidth), "…")
		data = append(data, []string{"", dimStyle.Render(fmt.Sprintf("+%d", hidden)), dimStyle.Render(more)})
	}
	for _, rec := range recs {
		var worker *look
		if rec.running {
			l := workerLook(rec.status)
			worker = &l
		}
		r := rec.row
		r.id = ansi.Truncate(oneLine(r.id), idWidth, "…") // a worker files tickets, so an ID is untrusted
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
	// lineOnly: only the one-line form for a short pane shows it; the strip has the load line above it,
	// which does (the workers).
	lineOnly bool
}

// totals are the run's counted totals: the tickets completed, set aside and waiting on the
// maintainer, and the running workers, which the strip leaves to the load line.
func (m Dashboard) totals() []total {
	var verdicts string
	if m.triaged > 0 {
		verdicts = organStyle.Render(fmt.Sprintf(" ◆ %d", m.triaged))
	}
	return []total{
		{label: "Completed", mark: "✓", n: m.closed, style: closedStyle},
		{label: "Set aside", mark: "↷", n: m.setAside(), style: deferredStyle, extra: verdicts},
		{label: "Needs you", mark: "?", n: m.needsYou(), style: stopStyle},
		{label: "Workers", mark: "▶", n: m.inSlots(), style: pickedStyle, of: m.slots(), lineOnly: true},
	}
}

// inSlots is how many of the running tickets have their workers at work, in the run's slots. One
// merging, waiting to merge or handed back to its worker to resolve or fix its branch isn't counted:
// it frees its slot as it goes to merge, and the Current list shows it beside them.
func (m Dashboard) inSlots() int {
	n := 0
	for _, st := range m.Running() {
		if st.Merge == dispatch.MergeNone && !st.Resolving && !st.Fixing {
			n++
		}
	}
	return n
}

// cell is t in the strip: a zero faint, the others with their mark.
func (t total) cell() string {
	if t.n == 0 {
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
	if m.queue.Queued < 0 {
		return "—", ""
	}
	if m.queue.Unclosed >= 0 {
		of = fmt.Sprintf(" of %d", m.queue.Unclosed)
	}
	return fmt.Sprint(m.queue.Queued), of
}

// heldBack is what follows the queue's count while ready tickets are held back, waiting for
// something besides a slot (an overlap with a running ticket, their subtickets, a ticket to merge, a
// solo ticket): " · 4 held back", and those whose worktree's setup failed, waiting for a ticket to
// merge or a probe: " · 1 setup failed"; or "" with none.
func (m Dashboard) heldBack() string {
	if m.queue.Queued < 0 {
		return ""
	}
	var s string
	if n := m.queue.Held.Total() - m.queue.Held.Setup; n > 0 {
		s = heldBackStyle.Render(fmt.Sprintf(" · %d held back", n))
	}
	if n := m.queue.Held.Setup; n > 0 {
		s += deferredStyle.Render(fmt.Sprintf(" · %d setup failed", n))
	}
	return s
}

// queueRoom is the width the strip keeps for In queue's count whatever it says now, so that the strip
// doesn't give way to the one-line form, a line shorter, as the count grows from "—" to
// "12 of 345 · held", and the tickets table below doesn't move.
const queueRoom = 16

// statsTable is the run's totals as one strip: a column per total, label over value, in the
// events' colours; the workers are on the load line above it. Too narrow a pane gets the one-line
// form instead, without them too.
func (m Dashboard) statsTable(w int) string {
	var labels, values []string
	for _, t := range m.totals() {
		if !t.lineOnly {
			labels, values = append(labels, t.label), append(values, t.cell())
		}
	}
	n, of := m.queueCount()
	queued := n + dimStyle.Render(of)
	if m.queue.Queued <= 0 { // "—" or an empty queue: faint, like the strip's other zeros
		queued = dimStyle.Render(n + of)
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
	need += max(queueRoom-ansi.StringWidth(queued), 0)
	if w < need {
		return m.totalsLine(w, false)
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

// statsLine is the totals on one line, the workers among them, for a pane too short for the strip and
// the load line. The branch, running time and stopping state are on the title line, the winding down
// above the hint.
func (m Dashboard) statsLine(w int) string { return m.totalsLine(w, true) }

// totalsLine is the totals on one line, the workers among them or, under the load line, which has
// them, not.
func (m Dashboard) totalsLine(w int, workers bool) string {
	var parts []string
	for _, t := range m.totals() {
		if workers || !t.lineOnly {
			parts = append(parts, t.part())
		}
	}
	n, of := m.queueCount()
	queue := dimStyle.Render("queue "+n+of) + m.heldBack()
	if m.draining {
		queue += windDownStyle.Render(" held") // not taken while the run winds down
	}
	parts = append(parts, queue)
	return ansi.Truncate(" "+strings.Join(parts, dimStyle.Render(" · ")), w, "…")
}
