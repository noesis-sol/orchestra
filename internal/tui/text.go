package tui

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// stack joins the parts that aren't empty, top to bottom.
func stack(parts ...string) string {
	return lipgloss.JoinVertical(lipgloss.Left, slices.DeleteFunc(parts, func(p string) bool { return p == "" })...)
}

// capitalize is s with its first letter, which may take more than a byte, in upper case.
func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if size == 0 || r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
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

// boxChrome is the columns a box takes besides its content: its rounded border and a space of
// padding on each side.
const boxChrome = 4

// boxInner is the room for the content of a box w wide.
func boxInner(w int) int { return w - boxChrome }

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
// longer than the width. A character wider than the width, one that no cut fits, gets a line of its own.
func wordWrap(s string, width int) []string {
	var lines []string
	line := ""
	for word := range strings.FieldsSeq(s) {
		for word != "" && ansi.StringWidth(word) > width { // a word too long for any line
			if line != "" {
				lines, line = append(lines, line), ""
			}
			cut := ansi.Truncate(word, width, "")
			if cut == "" {
				_, size := utf8.DecodeRuneInString(word)
				cut = word[:size]
			}
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

// oneLine collapses the whitespace in s, line breaks included, to single spaces: text the dashboard
// shows on one line. It only lays text out: text from a ticket, a worker or an organ is made
// printable where it is received (see printable.go), or with Printable before it is laid out.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// Printable is s, text from a ticket, a worker or an organ, without what a terminal would act on
// rather than show: escape sequences (one that writes the clipboard, clears the screen or sets the
// window title), the other control characters but line breaks and tabs, and bytes that aren't UTF-8.
// A carriage return becomes a line break, so that it can't take a printed line back to its start.
func Printable(s string) string {
	s = strings.ReplaceAll(strings.ToValidUTF8(s, ""), "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\r':
			return '\n'
		case r == '\n' || r == '\t' || !unicode.IsControl(r):
			return r
		}
		return -1
	}, ansi.Strip(s))
}

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
