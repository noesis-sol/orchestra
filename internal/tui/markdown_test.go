package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

const reviewReport = `# Orchestra run · 2026-10-15 10:41–10:52 · main

The run ended when the ready queue emptied
after both dispatched tickets were merged into main.

## Finished
- orchestra-7wok: the run report lists under Needs you only what the run itself flagged, with a test (a0135fd).
- orchestra-fpt2: orchestra now closes an epic once a run merges its last subticket (57b913d).

## Set aside
Nothing.

## Needs you
Nothing.
`

// On a terminal the report shows its headings without their hashes, its bullets as bullets wrapped
// under their text, its paragraphs joined and wrapped to the width, in colour.
func TestReportRendersMarkdownOnATerminal(t *testing.T) {
	styled(t)
	var b strings.Builder
	Printer{Out: &b, Styled: true, Width: 60}.Report(reviewReport)
	out := b.String()
	if !sgr.MatchString(out) {
		t.Errorf("the report has no colours:\n%s", out)
	}
	plain := ansi.Strip(out)
	want := `
Orchestra run · 2026-10-15 10:41–10:52 · main

The run ended when the ready queue emptied after both
dispatched tickets were merged into main.

Finished

• orchestra-7wok: the run report lists under Needs you only
  what the run itself flagged, with a test (a0135fd).
• orchestra-fpt2: orchestra now closes an epic once a run
  merges its last subticket (57b913d).

Set aside

Nothing.

Needs you

Nothing.
`
	if plain != want {
		t.Errorf("report rendered as\n%s\nwant\n%s", plain, want)
	}
	for line := range strings.SplitSeq(out, "\n") {
		if w := ansi.StringWidth(line); w > 60 {
			t.Errorf("a line %d columns wide on a terminal of 60: %q", w, line)
		}
	}
}

// Inline Markdown loses its markup and keeps its text: code, strong, emphasis, a link's text and target,
// escaped characters, and the asterisks and underscores that open nothing.
func TestMarkdownInlineText(t *testing.T) {
	for md, want := range map[string]string{
		"run `go test ./...` now":            "run go test ./... now",
		"a **bold** and *slanted* word":      "a bold and slanted word",
		"__bold__ and _slanted_":             "bold and slanted",
		"snake_case_name and file_name.go":   "snake_case_name and file_name.go",
		"2 * 3 * 4 and a lone *":             "2 * 3 * 4 and a lone *",
		`a \*literal\* star`:                 "a *literal* star",
		"see [the docs](https://x.y/docs)":   "see the docs https://x.y/docs",
		"<https://x.y> and [x.y](x.y)":       "<https://x.y> and x.y",
		"``a `tick` inside``":                "a `tick` inside",
		"an unclosed `tick":                  "an unclosed `tick",
		"**bold with `code` and *slant***":   "bold with code and slant",
		"[link **text**](u \"title\") after": "link text u after",
	} {
		if got := ansi.Strip(inline(md)); got != want {
			t.Errorf("inline(%q) = %q, want %q", md, got, want)
		}
	}
}

// Emphasis is styled on a terminal, and code apart from the text around it.
func TestMarkdownInlineStyles(t *testing.T) {
	styled(t)
	if got := inline("a **b** c"); !strings.Contains(got, "\x1b[1mb\x1b[0m") {
		t.Errorf("strong text not bold: %q", got)
	}
	if got := inline("a *b* c"); !strings.Contains(got, "\x1b[3mb\x1b[0m") {
		t.Errorf("emphasis not italic: %q", got)
	}
	if got := inline("a `b` c"); !sgr.MatchString(got) {
		t.Errorf("code not styled: %q", got)
	}
}

// Code blocks and tables keep their lines as written; lists keep their numbers and nesting; quotes
// and rules are drawn.
func TestMarkdownBlocks(t *testing.T) {
	md := "Intro line\n" +
		"```go\nfunc main() {\n\treturn\n}\n```\n" +
		"| a | b |\n|---|---|\n| 1 | 2 |\n\n" +
		"1. first\n2. second\n  - nested\n\n" +
		"> quoted\n> on\n\n" +
		"---\n" +
		"### Third ###\n" +
		"#hashtag is text"
	want := `Intro line

  func main() {
      return
  }

| a | b |
|---|---|
| 1 | 2 |

1. first
2. second
  • nested

│ quoted on

────────────────────────────────────────

Third

#hashtag is text
`
	if got := ansi.Strip(renderMarkdown(md, 80)); got != want {
		t.Errorf("rendered as\n%s\nwant\n%s", got, want)
	}
}

// A word longer than the line is cut rather than left to overflow, and a narrow terminal still gets
// lines of some width.
func TestMarkdownWrapsLongWords(t *testing.T) {
	out := renderMarkdown("- "+strings.Repeat("x", 50), 30)
	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		if w := ansi.StringWidth(line); w > 30 {
			t.Errorf("a line %d columns wide on a terminal of 30: %q", w, line)
		}
		if !strings.HasPrefix(line, "• ") && !strings.HasPrefix(line, "  ") {
			t.Errorf("a wrapped item's line is not under its text: %q", line)
		}
	}
	if out := renderMarkdown("a b c", 0); out != "a b c\n" {
		t.Errorf("on a terminal of unknown width: %q", out)
	}
}
