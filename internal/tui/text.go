package tui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// stack joins the parts that aren't empty, top to bottom.
func stack(parts ...string) string {
	return lipgloss.JoinVertical(lipgloss.Left, slices.DeleteFunc(parts, func(p string) bool { return p == "" })...)
}

// wrapAround word-wraps lead + list + tail to width, continuation lines starting with indent,
// and shortens list, kept on one line with the punctuation touching it, where it doesn't fit.
func wrapAround(lead, list, tail string, width int, indent string) []string {
	var lines []string
	line, fresh := "", true // fresh: nothing on the line but its indent
	add := func(word string) {
		switch {
		case fresh:
			line += word
		case ansi.StringWidth(line)+1+ansi.StringWidth(word) <= width:
			line += " " + word
		default:
			lines, line = append(lines, line), indent+word
		}
		fresh = false
	}
	if strings.HasPrefix(lead, " ") { // keep a leading margin, which Fields drops
		line = lead[:len(lead)-len(strings.TrimLeft(lead, " "))]
	}
	before, after := strings.Fields(lead), strings.Fields(tail)
	left, right := "", ""
	if list != "" && len(before) > 0 && !strings.HasSuffix(lead, " ") {
		left, before = before[len(before)-1], before[:len(before)-1]
	}
	if list != "" && len(after) > 0 && !strings.HasPrefix(tail, " ") {
		right, after = after[0], after[1:]
	}
	for _, word := range before {
		add(word)
	}
	if list != "" {
		unit := left + list + right
		room := width - ansi.StringWidth(line) - 1
		if fresh {
			room++
		}
		const least = 6 // fewer columns of IDs say nothing
		if ansi.StringWidth(unit) > room && room-ansi.StringWidth(left+right) < min(least, ansi.StringWidth(list)) {
			lines, line, fresh = append(lines, line), indent, true
			room = width - ansi.StringWidth(indent)
		}
		if ansi.StringWidth(unit) > room {
			unit = left + ansi.Truncate(list, max(room-ansi.StringWidth(left+right), 1), "…") + right
		}
		add(unit)
	}
	for _, word := range after {
		add(word)
	}
	lines = append(lines, line)
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…") // a word wider than the pane
	}
	return lines
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// overlay draws fg over the middle of bg, a view w wide.
func overlay(bg, fg string, w int) string {
	lines, over := strings.Split(bg, "\n"), strings.Split(fg, "\n")
	fw := lipgloss.Width(fg)
	left, top := max((w-fw)/2, 0), max((len(lines)-len(over))/2, 0)
	for i, l := range over {
		if top+i >= len(lines) {
			break
		}
		b := lines[top+i]
		head := ansi.Truncate(b, left, "")
		head += strings.Repeat(" ", max(left-ansi.StringWidth(head), 0))
		lines[top+i] = head + "\x1b[0m" + l + "\x1b[0m" + rightOf(b, left+fw)
	}
	return strings.Join(lines, "\n")
}

// rightOf is what shows of line b right of column end. A wide character the column cuts in half
// becomes a space, so the line keeps b's width.
func rightOf(b string, end int) string {
	want := max(ansi.StringWidth(b)-end, 0)
	tail := ansi.TruncateLeft(b, end, "")
	for cut := end + 1; ansi.StringWidth(tail) > want; cut++ {
		tail = ansi.TruncateLeft(b, cut, "")
	}
	return strings.Repeat(" ", want-ansi.StringWidth(tail)) + tail
}

// box frames content in a rounded border, w columns wide and h lines tall, borders included; h 0 is
// as tall as the content.
func box(w, h int, border lipgloss.TerminalColor, content string) string {
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(w - 2)
	if h > 0 {
		style = style.Height(h - 2)
	}
	return style.Render(content)
}

// wordWrap breaks s between words only (not at hyphens, as in worker-prompt.md), cutting a word
// longer than the width.
func wordWrap(s string, width int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(s) {
		for ansi.StringWidth(word) > width { // a word too long for any line
			if line != "" {
				lines, line = append(lines, line), ""
			}
			cut := ansi.Truncate(word, width, "")
			lines, word = append(lines, cut), word[len(cut):]
		}
		switch {
		case line == "":
			line = word
		case ansi.StringWidth(line)+1+ansi.StringWidth(word) <= width:
			line += " " + word
		default:
			lines, line = append(lines, line), word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}

// oneLine collapses the whitespace in s, line breaks included, to single spaces: text from a
// ticket, a worker or an organ that the dashboard shows on one line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// wrapLines word-wraps s to width and keeps at most limit lines, ending the last with … if cut.
func wrapLines(s string, width, limit int) []string {
	s = oneLine(s)
	if s == "" || width < 1 {
		return nil
	}
	lines := wordWrap(s, width)
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " ")
	}
	if len(lines) > limit {
		rest := strings.Join(lines[limit-1:], " ")
		lines = append(lines[:limit-1], ansi.Truncate(rest, width, "…"))
	}
	return lines
}
