package dispatch

import (
	"slices"
	"testing"
)

// trackedUnicode is trackedHere with files whose names have letters outside ASCII, résumé.md with
// its é as e and a combining accent (U+0301), as macOS often writes it.
var trackedUnicode = append(slices.Clone(trackedHere), "docs/café.md", "docs/über.md", "docs/日本語.md",
	"docs/résumé.md", "docs/中文README.md", "scripts/vérifier.sh")

// A path with letters or marks outside ASCII is one word, found among the repository's files or
// kept whole without them.
func TestTicketFootprintFindsNonASCIIPaths(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		text string
		want []string
	}{
		{"Fix the typo in docs/café.md.", []string{"docs/café.md"}},
		{"Reword (docs/über.md) and “docs/日本語.md”.", []string{"docs/über.md", "docs/日本語.md"}},
		{"Rewrite docs/résumé.md", []string{"docs/résumé.md"}},
		{"Shorten 日本語.md", []string{"docs/日本語.md"}}, // a bare name, as with an ASCII one
		// The word names a file, so README.md, an ASCII part of it, isn't taken for another.
		{"Translate docs/中文README.md", []string{"docs/中文README.md"}},
	} {
		if fp := TicketFootprint(Ticket{Description: c.text}, trackedUnicode, ""); !slices.Equal(fp.Files, c.want) {
			t.Errorf("%q: files %q, want %q", c.text, fp.Files, c.want)
		}
	}

	// Without the repository's files, the path is kept whole, not cut into docs/caf and .md; an
	// absolute path still gives nothing.
	fp := TicketFootprint(Ticket{Description: "Fix docs/café.md, not /Users/me/café.go."}, nil, "")
	if want := []string{"docs/café.md"}; !slices.Equal(fp.Files, want) {
		t.Errorf("unknown repository: files %q, want %q", fp.Files, want)
	}

	// The check command's files are found the same way.
	only := Ticket{Title: "Say hello", AcceptanceCriteria: "scripts/vérifier.sh passes"}
	if fp := TicketFootprint(only, trackedUnicode, "sh scripts/vérifier.sh"); !fp.Empty() {
		t.Errorf("a ticket naming only the check script: footprint %q, want empty", fp)
	}
}

// A path run into words of a script written without spaces, which pathToken takes into the same
// word, is still found: the word names nothing, so its ASCII parts are tried, as before.
func TestTicketFootprintFindsPathsInTextWithoutSpaces(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		text         string
		files, funcs []string
	}{
		{"loop.goを直す", []string{"internal/dispatch/loop.go"}, nil},
		{"修改internal/tui/run.go中的函数", []string{"internal/tui/run.go"}, nil},
		{"merge.goとclaude.goを直す", []string{"internal/claude/claude.go", "internal/dispatch/merge.go"}, nil},
		{"Loop.mergeを直す", nil, []string{"Loop.merge"}},
	} {
		fp := TicketFootprint(Ticket{Description: c.text}, trackedUnicode, "")
		if !slices.Equal(fp.Files, c.files) || !slices.Equal(fp.Funcs, c.funcs) {
			t.Errorf("%q: files %q, functions %q; want %q, %q", c.text, fp.Files, fp.Funcs, c.files, c.funcs)
		}
	}
}
