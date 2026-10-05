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
		if st.Agent == dispatch.StateBlocked {
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
// when the run winds down with nothing running. In a pane gridWidth wide or wider the boxes go two to
// a row, numbered row by row, the two in a row as tall as the taller; an odd one out keeps the left
// column's width. A narrower pane stacks them.
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
	panel := func(i, width, h int) string { return m.workerPanel(width, h, i+1, running[i], lines) }
	var boxes []string
	if w < gridWidth {
		for i := range running {
			boxes = append(boxes, panel(i, w, 0))
		}
		return lipgloss.JoinVertical(lipgloss.Left, boxes...)
	}
	left := (w - 1) / 2 // box(w) is w wide: two boxes and a one-column gap make the pane's width
	right := w - 1 - left
	for i := 0; i < len(running); i += 2 {
		if i+1 == len(running) {
			boxes = append(boxes, panel(i, left, 0))
			break
		}
		a, b := panel(i, left, 0), panel(i+1, right, 0)
		switch h := max(lipgloss.Height(a), lipgloss.Height(b)); {
		case lipgloss.Height(a) < h:
			a = panel(i, left, h)
		case lipgloss.Height(b) < h:
			b = panel(i+1, right, h)
		}
		boxes = append(boxes, lipgloss.JoinHorizontal(lipgloss.Top, a, " ", b))
	}
	return lipgloss.JoinVertical(lipgloss.Left, boxes...)
}

// workerPanel boxes the nth running ticket, w wide and h tall (0: as tall as it needs): its number,
// ID, worker status and time, title, latest action.
func (m Dashboard) workerPanel(w, h, n int, st dispatch.Status, titleMax int) string {
	inner := w - 4 // rounded border and one space of padding on each side
	fit := func(s string) string { return ansi.Truncate(s, inner, "…") }
	lines := []string{fit(m.workerHead(n, st))}
	for _, l := range wrapLines(st.Title, inner-2, titleMax) {
		lines = append(lines, "  "+l)
	}
	if st.Activity != "" {
		lines = append(lines, fit("  "+dimStyle.Render(oneLine(st.Activity))))
	}
	return box(w, h, workerBorder(st), strings.Join(lines, "\n"))
}

// titleLines is how many lines the active ticket's title may take before it is cut short.
const titleLines = 3

// doingLabel is the worker's status, made precise by what it reported doing when it is working.
func doingLabel(st dispatch.Status) string {
	switch {
	case st.Resolving:
		return "resolving"
	case st.Unreadable:
		return "unreadable"
	case st.Agent == dispatch.StateWorking && st.Doing != "":
		return st.Doing
	}
	return string(st.Agent)
}

func agentStyle(s string) string {
	switch s {
	case string(dispatch.StateWorking), "editing", "reading":
		return pickedStyle.Render(s)
	case "testing":
		return testingStyle.Render(s)
	case string(dispatch.StateBlocked): // red, as a ticket blocked from merging reads in the tickets table
		return stopStyle.Render(s + " — waiting for you")
	case "":
		return ""
	}
	return deferredStyle.Render(s)
}
