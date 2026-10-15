package tui

import (
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The run report's Markdown, rendered for a terminal. It is the Markdown an organ writes: headings,
// paragraphs, lists, quotes, code and emphasis, which this renders line by line rather than with a
// full Markdown library. Glamour, which it replaces, pulled in Chroma's lexers and styles, whose
// package init cost every run of the binary several milliseconds, a worker's hook included.

var (
	h1Style    = lipgloss.NewStyle().Foreground(purple).Bold(true)
	h2Style    = lipgloss.NewStyle().Foreground(cyan).Bold(true)
	hNStyle    = lipgloss.NewStyle().Foreground(chalk).Bold(true) // the third level and below
	codeStyle  = lipgloss.NewStyle().Foreground(pink)
	quoteStyle = lipgloss.NewStyle().Faint(true).Italic(true)
	linkStyle  = lipgloss.NewStyle().Foreground(blue) // not underlined: lipgloss underlines rune by rune
	urlStyle   = lipgloss.NewStyle().Faint(true)
)

var (
	headingLine = regexp.MustCompile(`^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)
	bulletLine  = regexp.MustCompile(`^( *)[-*+][ \t]+(.*)$`)
	orderedLine = regexp.MustCompile(`^( *)(\d{1,9}[.)])[ \t]+(.*)$`)
	ruleLine    = regexp.MustCompile(`^ {0,3}(?:(?:-[ \t]*){3,}|(?:\*[ \t]*){3,}|(?:_[ \t]*){3,})$`)
	fenceLine   = regexp.MustCompile("^ {0,3}(```+|~~~+)")
	quoteLine   = regexp.MustCompile(`^ {0,3}>[ \t]?(.*)$`)
)

// mdBlock is a block of the Markdown: a heading, a paragraph, a list item, a quote, a rule, or lines
// kept as they are (code, a table).
type mdBlock struct {
	kind   mdKind
	level  int    // a heading's level
	marker string // a list item's bullet or number
	indent int    // a list item's depth, in columns
	text   string // the inline text, its lines joined by spaces
	lines  []string
	gap    bool // a blank line before it in the Markdown
}

type mdKind int

const (
	mdParagraph mdKind = iota
	mdHeading
	mdItem
	mdQuote
	mdRule
	mdVerbatim // a fenced code block or a table, shown line by line
)

// renderMarkdown renders md for a terminal width columns wide, word-wrapped, ending with a line break.
func renderMarkdown(md string, width int) string {
	width = max(width, 20)
	var out []string
	var prev *mdBlock
	for _, b := range mdBlocks(md) {
		if prev != nil && (b.gap || b.kind != mdItem || prev.kind != mdItem) {
			out = append(out, "") // only a list's items go on without one
		}
		out = append(out, b.render(width)...)
		prev = &b
	}
	return strings.Join(out, "\n") + "\n"
}

// mdBlocks splits md into its blocks.
func mdBlocks(md string) []mdBlock {
	var blocks []mdBlock
	gap := false
	last := func(kinds ...mdKind) *mdBlock { // the block before, without a gap, if of one of kinds
		if gap || len(blocks) == 0 || !slices.Contains(kinds, blocks[len(blocks)-1].kind) {
			return nil
		}
		return &blocks[len(blocks)-1]
	}
	add := func(b mdBlock) {
		b.gap, gap = gap && len(blocks) > 0, false
		blocks = append(blocks, b)
	}
	lines := strings.Split(strings.ReplaceAll(md, "\t", "    "), "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			gap = true
			continue
		}
		if m := fenceLine.FindStringSubmatch(line); m != nil {
			code := mdBlock{kind: mdVerbatim}
			for i++; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), m[1]); i++ {
				code.lines = append(code.lines, lines[i])
			}
			add(code)
			continue
		}
		if m := headingLine.FindStringSubmatch(line); m != nil {
			add(mdBlock{kind: mdHeading, level: len(m[1]), text: m[2]})
			continue
		}
		if ruleLine.MatchString(line) {
			add(mdBlock{kind: mdRule})
			continue
		}
		if m := bulletLine.FindStringSubmatch(line); m != nil {
			add(mdBlock{kind: mdItem, indent: len(m[1]), marker: "•", text: m[2]})
			continue
		}
		if m := orderedLine.FindStringSubmatch(line); m != nil {
			add(mdBlock{kind: mdItem, indent: len(m[1]), marker: m[2], text: m[3]})
			continue
		}
		if m := quoteLine.FindStringSubmatch(line); m != nil {
			if b := last(mdQuote); b != nil {
				b.text += " " + m[1]
			} else {
				add(mdBlock{kind: mdQuote, text: m[1]})
			}
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "|") {
			if b := last(mdVerbatim); b != nil && b.marker == "|" {
				b.lines = append(b.lines, line)
			} else {
				add(mdBlock{kind: mdVerbatim, marker: "|", lines: []string{line}})
			}
			continue
		}
		if b := last(mdParagraph, mdItem); b != nil { // a paragraph's or an item's next line
			b.text += " " + strings.TrimSpace(line)
			continue
		}
		add(mdBlock{kind: mdParagraph, text: strings.TrimSpace(line)})
	}
	return blocks
}

// render is b's lines, wrapped to width.
func (b mdBlock) render(width int) []string {
	switch b.kind {
	case mdHeading:
		style := hNStyle
		switch b.level {
		case 1:
			style = h1Style
		case 2:
			style = h2Style
		}
		return wrapped(style.Render(plainInline(b.text)), width, "", "")
	case mdItem:
		lead := strings.Repeat(" ", min(b.indent, width/2)) + b.marker + " "
		return wrapped(inline(b.text), width, lead, strings.Repeat(" ", ansi.StringWidth(lead)))
	case mdQuote:
		bar := dimStyle.Render("│ ")
		return wrapped(quoteStyle.Render(plainInline(b.text)), width, bar, bar)
	case mdRule:
		return []string{dimStyle.Render(strings.Repeat("─", min(width, 40)))}
	case mdVerbatim:
		lines := make([]string, len(b.lines))
		for i, l := range b.lines {
			if b.marker == "|" {
				lines[i] = l
			} else {
				lines[i] = codeStyle.Render("  " + l)
			}
		}
		return lines
	}
	return wrapped(inline(b.text), width, "", "")
}

// wrapped word-wraps text to width after the first line's lead and the others' indent, both of the
// same width.
func wrapped(text string, width int, lead, indent string) []string {
	lines := strings.Split(ansi.Wrap(text, max(width-ansi.StringWidth(lead), 10), ""), "\n")
	for i := range lines {
		pre := indent
		if i == 0 {
			pre = lead
		}
		lines[i] = pre + strings.TrimRight(lines[i], " ")
	}
	return lines
}

// span is a run of inline text in one style.
type span struct {
	text                     string
	bold, italic, code, link bool
	url                      string // a link's target, shown after its text when it differs
}

// inline renders a block's inline Markdown: code spans, strong and emphasis, links, escapes.
func inline(s string) string {
	var b strings.Builder
	for _, sp := range spans(s) {
		switch {
		case sp.code:
			b.WriteString(codeStyle.Render(sp.text))
		case sp.link:
			b.WriteString(linkStyle.Bold(sp.bold).Italic(sp.italic).Render(sp.text))
			if sp.url != "" && sp.url != sp.text {
				b.WriteString(" " + urlStyle.Render(sp.url))
			}
		case sp.bold || sp.italic:
			b.WriteString(lipgloss.NewStyle().Bold(sp.bold).Italic(sp.italic).Render(sp.text))
		default:
			b.WriteString(sp.text)
		}
	}
	return b.String()
}

// plainInline is s's inline text without its markup, for a block styled as a whole.
func plainInline(s string) string {
	var b strings.Builder
	for _, sp := range spans(s) {
		b.WriteString(sp.text)
		if sp.link && sp.url != "" && sp.url != sp.text {
			b.WriteString(" " + sp.url)
		}
	}
	return b.String()
}

// mdPunct are the characters a backslash escapes.
const mdPunct = "!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~"

// spans splits s into its styled runs. A delimiter opens only when it closes later in s, so that a
// lone asterisk or underscore stays as it is; an underscore opens and closes only at a word's edge,
// so that snake_case names do too.
func spans(s string) []span {
	var out []span
	var cur strings.Builder
	bold, italic := false, false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, span{text: cur.String(), bold: bold, italic: italic})
			cur.Reset()
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\\' && i+1 < len(s) && strings.IndexByte(mdPunct, s[i+1]) >= 0:
			cur.WriteByte(s[i+1])
			i += 2
			continue
		case c == '`':
			n := len(s[i:]) - len(strings.TrimLeft(s[i:], "`"))
			ticks := s[i : i+n]
			if end := strings.Index(s[i+n:], ticks); end >= 0 {
				flush()
				code := s[i+n : i+n+end]
				if t := strings.TrimSpace(code); t != "" {
					code = t
				}
				out = append(out, span{text: code, code: true})
				i += n + end + n
				continue
			}
			cur.WriteString(ticks)
			i += n
			continue
		case c == '[':
			if text, url, n, ok := link(s[i:]); ok {
				flush()
				out = append(out, span{text: plainInline(text), url: url, link: true, bold: bold, italic: italic})
				i += n
				continue
			}
		case c == '*' || c == '_':
			n := 1
			if i+1 < len(s) && s[i+1] == c {
				n = 2
			}
			delim := s[i : i+n]
			on := &italic
			if n == 2 {
				on = &bold
			}
			if *on && closes(s, i, n) {
				flush()
				*on = false
				i += n
				continue
			}
			if !*on && opens(s, i, n) && strings.Contains(s[i+n:], delim) {
				flush()
				*on = true
				i += n
				continue
			}
			cur.WriteString(delim)
			i += n
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		cur.WriteString(s[i : i+size])
		i += size
	}
	flush()
	return out
}

// opens says whether the delimiter of n characters at s[i] can open emphasis: followed by text, and
// for an underscore, not inside a word.
func opens(s string, i, n int) bool {
	next, _ := utf8.DecodeRuneInString(s[i+n:])
	if i+n >= len(s) || unicode.IsSpace(next) {
		return false
	}
	return s[i] != '_' || !wordBefore(s, i)
}

// closes says whether the delimiter of n characters at s[i] can close emphasis: after text, and for an
// underscore, not inside a word.
func closes(s string, i, n int) bool {
	if i == 0 {
		return false
	}
	if prev, _ := utf8.DecodeLastRuneInString(s[:i]); unicode.IsSpace(prev) {
		return false
	}
	if s[i] != '_' || i+n >= len(s) {
		return true
	}
	next, _ := utf8.DecodeRuneInString(s[i+n:])
	return !isWord(next)
}

func wordBefore(s string, i int) bool {
	if i == 0 {
		return false
	}
	prev, _ := utf8.DecodeLastRuneInString(s[:i])
	return isWord(prev)
}

func isWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// link reads a link, [text](url), at the start of s: its text, its target and its length.
func link(s string) (text, url string, n int, ok bool) {
	end := strings.Index(s, "](")
	if end < 0 || strings.Contains(s[1:end], "]") {
		return "", "", 0, false
	}
	paren := strings.IndexByte(s[end+2:], ')')
	if paren < 0 {
		return "", "", 0, false
	}
	url = strings.TrimSpace(s[end+2 : end+2+paren])
	if i := strings.IndexAny(url, " \t"); i >= 0 { // a title after the target
		url = url[:i]
	}
	return s[1:end], strings.Trim(url, "<>"), end + 2 + paren + 1, true
}
