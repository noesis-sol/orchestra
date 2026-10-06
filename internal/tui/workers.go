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

// current heads the worker boxes with a Current label, indented like the tickets table's Tickets
// header but bold in the working colour, where the eye should land; nothing when there are no boxes.
func current(panels string) string {
	if panels == "" {
		return ""
	}
	return lipgloss.JoinVertical(lipgloss.Left, "  "+pickedStyle.Render("Current"), panels)
}

// workerList is the compact form of the worker boxes: one box, one line per running worker.
func (m Dashboard) workerList(w int) string {
	running := m.Running()
	if len(running) == 0 {
		return m.workerPanels(w)
	}
	var lines []string
	for i, st := range running {
		lines = append(lines, ansi.Truncate(m.workerHead(i+1, st)+"  "+oneLine(st.Title), w-4, "…"))
	}
	return box(w, 0, workerBorder(running...), strings.Join(lines, "\n"))
}

// numbered is how many running workers get a number, the key that goes to their tab.
const numbered = 9

// workerHead is the nth running worker's first line: its number, ID, worker status and time. A
// worker beyond the ninth gets no number, only the room for one.
func (m Dashboard) workerHead(n int, st dispatch.Status) string {
	num := "  "
	if n <= numbered {
		num = keyStyle.Render(fmt.Sprint(n)) + " "
	}
	elapsed := time.Since(st.Started).Truncate(time.Second)
	return fmt.Sprintf("%s%s %s  %s  %s", num, m.spin.View(), pickedStyle.Render(st.Ticket),
		agentStyle(doingLabel(st)), dimStyle.Render(elapsed.String()))
}

// goTo switches Herdr to the tab of the worker numbered n in Current, as the list stands now:
// nothing if there is none or it has no tab yet. The callback runs Herdr in the background.
func (m Dashboard) goTo(n int) {
	if running := m.Running(); n <= min(len(running), numbered) && running[n-1].Tab != "" {
		m.focus(running[n-1].Tab)
	}
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
	for _, st := range m.active {
		l = append(l, st)
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
	lines := titleLines
	if len(running) > 1 {
		lines = 2 // keep several boxes within the pane
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
	h := 0
	for i, st := range running {
		h = max(h, lipgloss.Height(m.workerPanel(width(i), 0, i+1, st, lines)))
	}
	var boxes []string
	for i, st := range running {
		boxes = append(boxes, m.workerPanel(width(i), h, i+1, st, lines))
	}
	if w < gridWidth {
		return lipgloss.JoinVertical(lipgloss.Left, boxes...)
	}
	var rows []string
	for i := 0; i < len(boxes); i += 2 {
		if i+1 == len(boxes) {
			rows = append(rows, boxes[i])
			break
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, boxes[i], " ", boxes[i+1]))
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// workerPanel boxes the nth running ticket, w wide and h tall (0: as tall as it needs): its number,
// ID, worker status and time, title, latest action, its line blank until the worker first reports.
func (m Dashboard) workerPanel(w, h, n int, st dispatch.Status, titleMax int) string {
	inner := w - 4 // rounded border and one space of padding on each side
	fit := func(s string) string { return ansi.Truncate(s, inner, "…") }
	lines := []string{fit(m.workerHead(n, st))}
	for _, l := range wrapLines(st.Title, inner-2, titleMax) {
		lines = append(lines, "  "+l)
	}
	lines = append(lines, fit("  "+dimStyle.Render(oneLine(st.Activity))))
	return box(w, h, workerBorder(st), strings.Join(lines, "\n"))
}

// titleLines is how many lines the active ticket's title may take before it is cut short.
const titleLines = 3

// doingLabel is the worker's status, made precise by what it reported doing when it is working,
// or by the permission prompt it reported waiting on, whatever Herdr shows.
func doingLabel(st dispatch.Status) string {
	switch {
	case st.Resolving:
		return "resolving"
	case st.Fixing:
		return "fixing check"
	case st.Unreadable:
		return "unreadable"
	case st.Permission:
		return permissionLabel
	case st.Agent == dispatch.StateWorking && st.Doing != "":
		return st.Doing
	}
	return string(st.Agent)
}

// permissionLabel is the status of a worker waiting on a permission prompt.
const permissionLabel = "permission"

func agentStyle(s string) string {
	switch s {
	case string(dispatch.StateWorking), "editing", "reading", dispatch.DoingSubagent:
		return pickedStyle.Render(s)
	case "testing":
		return testingStyle.Render(s)
	case string(dispatch.StateBlocked), permissionLabel: // red, as a ticket blocked from merging reads in the table
		return stopStyle.Render(s + " — waiting for you")
	case "":
		return ""
	}
	return deferredStyle.Render(s)
}
