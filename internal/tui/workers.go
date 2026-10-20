package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// withCurrentLabel heads the worker boxes with a Current label in the working colour, where the eye should
// land; nothing when there are no boxes.
func withCurrentLabel(panels string) string {
	if panels == "" {
		return ""
	}
	// Not joined to pad the label: the layout stacks it with the rest, which pads every line.
	return sectionLabel("Current", pickedStyle) + "\n" + panels
}

// sectionLabel is the line that heads a section of the dashboard, Tickets or Current: its name,
// indented and in style, which is bold.
func sectionLabel(name string, style lipgloss.Style) string { return "  " + style.Render(name) }

// currentListLines is how many lines Current takes in its one-line form (workerList) with every worker
// the run may have: a line each, in a box under the Current label.
func (m Dashboard) currentListLines() int { return m.slots() + sectionChrome }

// workerList is the compact form of the worker boxes: one box, one line per running worker, its ID,
// status, time and memory lined up in columns with the other lines', each as wide as the widest on
// screen.
func (m Dashboard) workerList(w int) string {
	running := m.Running()
	if len(running) == 0 {
		return m.workerPanels(w)
	}
	// Each row's cells are read once, so that the widths and what is drawn come from one reading of the
	// clock: a time that gains a digit between two readings would otherwise be wider than its column.
	cells := make([]headCells, len(running))
	var cols headColumns
	for i, st := range running {
		cells[i] = m.headCells(st)
		cols = cols.fit(cells[i])
	}
	var lines []string
	for i, st := range running {
		head := m.alignedHead(i+1, st, cells[i], cols)
		lines = append(lines, ansi.Truncate(head+"  "+oneLine(st.Title), boxInner(w), "…"))
	}
	return box(w, 0, workerBorder(running...), strings.Join(lines, "\n"))
}

// numbered is how many running workers get a number, the key that goes to their tab.
const numbered = 9

// workerHead is the nth running worker's first line: its number, ID, worker status, time and the
// memory its process tree takes, once read. A worker beyond the ninth gets no number, only the room
// for one.
func (m Dashboard) workerHead(n int, st dispatch.Status) string {
	return m.alignedHead(n, st, m.headCells(st), headColumns{})
}

// alignedHead is workerHead made of the cells c, its ID and status padded to cols and its time and
// memory right-aligned in cols, so that their digits line up with the other lines'.
func (m Dashboard) alignedHead(n int, st dispatch.Status, c headCells, cols headColumns) string {
	num := "  "
	if n <= numbered {
		num = keyStyle.Render(fmt.Sprint(n)) + " "
	}
	head := fmt.Sprintf("%s%s %s  %s  %s", num, m.mark(st), c.id+padding(c.id, cols.id),
		c.status+padding(c.status, cols.status), padding(c.time, cols.time)+c.time)
	if c.mem != "" || cols.mem > 0 {
		head += "  " + padding(c.mem, cols.mem) + c.mem
	}
	return head
}

// headCells are the cells of a worker's first line after its number and mark, in style.
type headCells struct{ id, status, time, mem string }

// headCells are the worker's ticket ID, worker status, time and memory ("" before it is read).
func (m Dashboard) headCells(st dispatch.Status) headCells {
	label := workerLook(st).status.render()
	if st.Merge == dispatch.MergeQueued && st.Place > 0 {
		label = toMergeStyle.Render(fmt.Sprintf("%s (%s)", toMergeLabel, ordinal(st.Place)))
	}
	var mem string
	if s := m.workerMemory(st.Ticket); s != "" {
		mem = dimStyle.Render(s)
	}
	return headCells{id: pickedStyle.Render(oneLine(st.Ticket)), status: label,
		time: dimStyle.Render(m.since(st.Started).Truncate(time.Second).String()), mem: mem}
}

// headColumns are the widths of the compact list's columns: the widest ID, status, time and memory on
// screen.
type headColumns struct{ id, status, time, mem int }

// fit widens the columns to hold c.
func (cols headColumns) fit(c headCells) headColumns {
	return headColumns{max(cols.id, lipgloss.Width(c.id)), max(cols.status, lipgloss.Width(c.status)),
		max(cols.time, lipgloss.Width(c.time)), max(cols.mem, lipgloss.Width(c.mem))}
}

// padding is the spaces that make s, styles not counted, w wide; none if it is as wide or wider.
func padding(s string, w int) string { return strings.Repeat(" ", max(0, w-lipgloss.Width(s))) }

// mark is what heads a worker's line: the spinner while its worker is at work, a hand-back included,
// and once it has finished, its look's static mark, so that motion means a worker at work. It is as
// wide as the spinner.
func (m Dashboard) mark(st dispatch.Status) string {
	if l := workerLook(st); l.mark.text != "" {
		return l.mark.render() + " "
	}
	return m.spin.View()
}

// ordinal is n as a place in line: 1st, 2nd, 3rd, 4th, …, 11th, 12th, 13th, 21st.
func ordinal(n int) string {
	suffix := "th"
	switch {
	case n%100 >= 11 && n%100 <= 13:
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	}
	return fmt.Sprint(n) + suffix
}

// tab is the tab of the worker numbered n in Current, as the list stands now, for its number key to
// switch to: "" if there is none, it has no tab yet, or the tabs are out of sight (no focus callback).
func (m Dashboard) tab(n int) string {
	if running := m.Running(); m.focus != nil && n <= min(len(running), numbered) {
		return running[n-1].Tab
	}
	return ""
}

// workerBorder colours a box of running workers red if any waits for the maintainer.
func workerBorder(running ...dispatch.Status) lipgloss.TerminalColor {
	for _, st := range running {
		if st.Agent == dispatch.StateBlocked || st.Permission {
			return red
		}
	}
	return cyan
}

// Running returns the running workers, oldest first; those started at the same time by ticket, so
// their numbers stay put.
func (m Dashboard) Running() []dispatch.Status {
	var l []dispatch.Status
	for _, r := range m.records {
		if r.section() == inCurrent {
			l = append(l, r.status)
		}
	}
	sort.Slice(l, func(i, j int) bool {
		if !l[i].Started.Equal(l[j].Started) {
			return l[i].Started.Before(l[j].Started)
		}
		return l[i].Ticket < l[j].Ticket
	})
	return l
}

// gridWidth is the narrowest pane whose worker boxes go two to a row: the full Herdr pane (~135
// columns) gets two boxes of ~67, the width a box has in the split pane, which keeps one column.
const gridWidth = 120

// workerPanels lays out a box per running worker, or one box saying what the loop is doing; nothing
// when the run winds down with nothing running. The boxes are all as tall as the tallest, which
// changes only as workers start and finish: each keeps a line for its latest action. In a pane
// gridWidth wide or wider the boxes go two to a row, numbered row by row; an odd one out keeps the
// left column's width. A narrower pane stacks them.
func (m Dashboard) workerPanels(w int) string {
	running := m.Running()
	if len(running) == 0 {
		if m.draining {
			return "" // the line above the hint says the run ends
		}
		msg := "picking the next ticket…"
		if m.stopping {
			msg = "stopping…"
		}
		return box(w, 0, grey, m.spin.View()+" "+dimStyle.Render(msg))
	}
	titleMax := titleLines
	if len(running) > 1 {
		titleMax = titleLinesShared
	}
	left := (w - 1) / 2 // box(w) is w wide: two boxes and a one-column gap make the pane's width
	width := func(i int) int {
		switch {
		case w < gridWidth:
			return w
		case i%2 == 0:
			return left
		}
		return w - 1 - left
	}
	// Each box is drawn as tall as it needs, then again as tall as the tallest if it is shorter.
	boxes := make([]string, len(running))
	h := 0
	for i, st := range running {
		boxes[i] = m.workerPanel(width(i), 0, i+1, st, titleMax)
		h = max(h, lipgloss.Height(boxes[i]))
	}
	for i, st := range running {
		if lipgloss.Height(boxes[i]) < h {
			boxes[i] = m.workerPanel(width(i), h, i+1, st, titleMax)
		}
	}
	if w < gridWidth {
		return strings.Join(boxes, "\n") // each box w wide: none to pad
	}
	var rows []string
	for i := 0; i < len(boxes); i += 2 {
		if i+1 == len(boxes) {
			// A box alone on its row, padded to the rows' width
			return lipgloss.JoinVertical(lipgloss.Left, append(rows, boxes[i])...)
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, boxes[i], " ", boxes[i+1]))
	}
	return strings.Join(rows, "\n") // each row w wide
}

// workerPanel boxes the nth running ticket, w wide and h tall (0: as tall as it needs): its number,
// ID, worker status and time, title, latest action, its line blank until the worker first reports.
func (m Dashboard) workerPanel(w, h, n int, st dispatch.Status, titleMax int) string {
	inner := boxInner(w)
	fit := func(s string) string { return ansi.Truncate(s, inner, "…") }
	lines := []string{fit(m.workerHead(n, st))}
	for _, l := range wrapLines(st.Title, inner-2, titleMax) {
		lines = append(lines, "  "+l)
	}
	lines = append(lines, fit("  "+dimStyle.Render(oneLine(st.Activity))))
	return box(w, h, workerBorder(st), strings.Join(lines, "\n"))
}

// How many lines the active ticket's title may take before it is cut short: in the only box, and in
// one of several, which keeps them within the pane.
const (
	titleLines       = 3
	titleLinesShared = 2
)

// styledText is text in its style; no text renders as nothing.
type styledText struct {
	text  string
	style lipgloss.Style
}

func (s styledText) render() string {
	if s.text == "" {
		return ""
	}
	return s.style.Render(s.text)
}

// look is how the dashboard shows a worker's status (workerStatus).
type look struct {
	status styledText // the status in the worker's line in Current
	row    styledText // its ticket's state in the tickets table; "" for working's (rowLooks)
	mark   styledText // what heads its line once its worker has finished; "" for the spinner
	spins  bool       // its worker is at work even when its agent's turn is done: a hand-back
}

// The statuses a worker has beyond its agent's state and what it is doing.
const (
	resolvingLabel  = "resolving"    // its rebase stopped on conflicts, handed back to it
	fixingLabel     = "fixing check" // its check failed on its rebased branch, handed back to it
	unreadableLabel = "unreadable"   // the last read of its agent's state failed
	permissionLabel = "permission"   // its hooks say it waits on a permission prompt
	// Its worker done with it, in the merge queue: waiting for its turn, and its merge under way.
	toMergeLabel = string(dispatch.MergeQueued)
	mergingLabel = string(dispatch.MergeRunning)
)

// waitingForYou is what follows the status of a worker waiting for the maintainer.
const waitingForYou = " — waiting for you"

// workerLooks are the worker statuses' looks, by status. A status with none is in the warning colour.
var workerLooks = map[string]look{
	resolvingLabel: {status: styledText{resolvingLabel, deferredStyle},
		row: styledText{"⟳ resolving", deferredStyle}, spins: true},
	fixingLabel: {status: styledText{fixingLabel, deferredStyle},
		row: styledText{"⟳ fixing", deferredStyle}, spins: true},
	toMergeLabel: {status: styledText{toMergeLabel, toMergeStyle},
		row: styledText{"◼ " + toMergeLabel, toMergeStyle}, mark: styledText{"◼", toMergeStyle}},
	mergingLabel: {status: styledText{mergingLabel, mergingStyle},
		row: styledText{"◆ " + mergingLabel, mergingStyle}, mark: styledText{"◆", mergingStyle}},
	unreadableLabel: {status: styledText{unreadableLabel, deferredStyle}},
	// Red, as a blocked worker's box reads; "permission" is too wide for the tickets table's column.
	permissionLabel: {status: styledText{permissionLabel + waitingForYou, stopStyle},
		row: styledText{"■ allow?", stopStyle}},
	string(dispatch.DoingTesting):  doingLook(dispatch.DoingTesting, testingStyle),
	string(dispatch.DoingEditing):  doingLook(dispatch.DoingEditing, pickedStyle),
	string(dispatch.DoingReading):  doingLook(dispatch.DoingReading, pickedStyle),
	string(dispatch.DoingSubagent): doingLook(dispatch.DoingSubagent, pickedStyle),
	string(dispatch.StateWorking):  {status: styledText{string(dispatch.StateWorking), pickedStyle}},
	string(dispatch.StateIdle):     {status: styledText{string(dispatch.StateIdle), deferredStyle}},
	// Red, as a ticket blocked from merging reads in the tickets table.
	string(dispatch.StateBlocked): {status: styledText{string(dispatch.StateBlocked) + waitingForYou, stopStyle}},
	// Finished its turn: green, not bold, as its ticket isn't merged yet.
	string(dispatch.StateDone): {status: styledText{string(dispatch.StateDone), doneStyle},
		mark: styledText{"•", doneStyle}},
	string(dispatch.StateUnknown): {status: styledText{string(dispatch.StateUnknown), deferredStyle}},
	"":                            {}, // Herdr hasn't said yet
}

// doingLook is the look of a working worker doing d: d in style, in Current and the tickets table.
func doingLook(d dispatch.Doing, style lipgloss.Style) look {
	return look{status: styledText{string(d), style}, row: styledText{"▶ " + string(d), style}}
}

// workerStatus is the worker's status, made precise by what it reported doing when it is working,
// or by the permission prompt it reported waiting on, whatever Herdr shows.
func workerStatus(st dispatch.Status) string {
	switch {
	case st.Resolving:
		return resolvingLabel
	case st.Fixing:
		return fixingLabel
	case st.Merge == dispatch.MergeQueued:
		return toMergeLabel
	case st.Merge == dispatch.MergeRunning:
		return mergingLabel
	case st.Unreadable:
		return unreadableLabel
	case st.Permission:
		return permissionLabel
	case st.Agent == dispatch.StateWorking && st.Doing != "":
		return string(st.Doing)
	}
	return string(st.Agent)
}

// workerLook is how the dashboard shows the worker's status. A worker whose agent's turn is done
// has the done mark, whatever more precise status it has, unless it is at work on a hand-back.
func workerLook(st dispatch.Status) look {
	status := workerStatus(st)
	l, ok := workerLooks[status]
	if !ok {
		l = look{status: styledText{status, deferredStyle}}
	}
	if l.mark.text == "" && !l.spins && st.Agent == dispatch.StateDone {
		l.mark = workerLooks[string(dispatch.StateDone)].mark
	}
	return l
}
