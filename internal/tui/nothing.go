package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The message a run with nothing to run shows in place of the dashboard: everything is done, or
// nothing is ready, and what holds the tickets left.

// Nothing prints why the run has nothing to run (see dispatch.CheckNothingToRun): on a terminal in
// a rounded box, as the dashboard draws its panels, fitting the terminal's width; otherwise the
// same lines, plain, after the time, as Say prints them.
func (p Printer) Nothing(n dispatch.NothingToRun) {
	if p.Styled {
		fmt.Fprintln(p.Out, nothingBox(n, p.Width))
		return
	}
	head, rest := nothingLines(n, false)
	p.sayPlain(head)
	for _, l := range rest {
		p.sayPlain("  " + l)
	}
}

// nothingLines is the message: its heading, and the lines below it, styled for a terminal or
// plain.
func nothingLines(n dispatch.NothingToRun, styled bool) (string, []string) {
	style := func(s lipgloss.Style, text string) string {
		if styled {
			return s.Render(text)
		}
		return text
	}
	bold := lipgloss.NewStyle().Bold(true)
	command := func(c string) string { return style(keyStyle, c) }
	var rest []string
	if n.AllDone {
		if len(n.StillOpen) > 0 {
			rest = append(rest, fmt.Sprintf("Still open: %s (%s)", strings.Join(n.StillOpen, ", "),
				command("bd close "+strings.Join(n.StillOpen, " "))))
		}
		return style(closedStyle, "✓") + " " + style(bold, "All done"), rest
	}
	heading := "Nothing ready to run"
	if n.Scope != "" {
		heading = "Nothing under " + n.Scope + " is ready to run"
	}
	head := style(dimStyle, "○") + " " + style(bold, heading)
	if n.Scope != "" {
		for _, d := range n.NotDone {
			rest = append(rest, style(pickedStyle, d.ID)+" ("+d.Why+")")
		}
		return head, rest
	}
	if n.Questions > 0 {
		rest = append(rest, plural(n.Questions, "question waits", "questions wait")+" for your answer: "+
			command("bd human list"))
	}
	if n.Waiting > 0 {
		rest = append(rest, plural(n.Waiting, "ticket waits", "tickets wait")+" on other tickets: "+command("bd blocked"))
	}
	if n.HeldParents > 0 {
		rest = append(rest, plural(n.HeldParents, "ticket waits for its subtickets", "tickets wait for their subtickets")+
			" to merge")
	}
	if n.HeldBlocked > 0 {
		rest = append(rest, plural(n.HeldBlocked, "ticket waits for its blocker", "tickets wait for their blockers")+
			" to merge")
	}
	var held []string
	for _, c := range []struct {
		n    int
		what string
	}{{n.InProgress, "in progress"}, {n.Deferred, "deferred"}, {n.Other, "other"}} {
		if c.n > 0 {
			held = append(held, fmt.Sprintf("%d %s", c.n, c.what))
		}
	}
	if len(held) > 0 {
		rest = append(rest, strings.Join(held, " · "))
	}
	if n.Unmerged > 0 {
		rest = append(rest, fmt.Sprintf("%d closed but not merged: %s", n.Unmerged, command("bd list --label unmerged")))
	}
	return head, rest
}

// plural is "1 <one>" or "n <many>".
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// nothingBox is the message in a rounded box, green when all is done, at most width wide (20 at
// the least) and no wider than its lines: a line too long for it wraps, indented under its own.
func nothingBox(n dispatch.NothingToRun, width int) string {
	head, rest := nothingLines(n, true)
	inner := ansi.StringWidth(head)
	for _, l := range rest {
		inner = max(inner, 2+ansi.StringWidth(l))
	}
	inner = min(inner, max(width, 20)-4) // the border and a space of padding on each side
	lines := hang(head, "", inner)
	for _, l := range rest {
		lines = append(lines, hang(l, "  ", inner)...)
	}
	border := grey
	if n.AllDone {
		border = green
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(inner + 2).Render(strings.Join(lines, "\n"))
}

// hang is line after indent, wrapped to width: the lines it wraps onto are indented two spaces
// more.
func hang(line, indent string, width int) []string {
	if len(indent)+ansi.StringWidth(line) <= width {
		return []string{indent + line}
	}
	wrapped := strings.Split(ansi.Wrap(line, max(width-len(indent)-2, 1), ""), "\n")
	for i := range wrapped {
		if i > 0 {
			wrapped[i] = "  " + wrapped[i]
		}
		wrapped[i] = indent + wrapped[i]
	}
	return wrapped
}
