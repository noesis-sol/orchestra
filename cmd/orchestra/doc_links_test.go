package main

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"unicode"
)

// The README and the docs link to one another's files and headings, and a heading renamed or a file moved leaves
// the links to it pointing nowhere, which GitHub shows as a 404 or as the top of the page. orchestra-hzyk fixed a
// link in docs/organs.md to a README heading a commit had removed, which nothing caught but a check by hand.
func TestDocLinksResolve(t *testing.T) {
	repo := os.DirFS("../..")
	docs, err := fs.Glob(repo, "docs/*.md")
	if err != nil || len(docs) == 0 {
		t.Fatalf("no docs/*.md: %v", err)
	}
	problems, err := docLinkProblems(repo, append([]string{"README.md"}, docs...))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// The check finds a link to a file that is missing or to a heading its file doesn't have, a link anywhere a reader
// can follow it, but not one inside code or to another site.
func TestDocLinkProblems(t *testing.T) {
	readme := strings.Join([]string{
		"# Title",
		"",
		"## Set up",
		"",
		"[Setup](docs/setup.md), [checks](docs/setup.md#the-checks), [again](docs/setup.md#the-checks-1 \"Again\"),",
		"[styled](docs/setup.md#go_vet-fast--linked), [back](#set-up), [script](scripts/x.sh#L3), [folder](docs/),",
		"[escaped](<docs/a%20b.md#title>), [from the root](/docs/setup.md#setup).",
		"[gone](docs/gone.md), [no heading](docs/setup.md#nowhere), [not here](#title-1),",
		"[fenced heading](docs/setup.md#not-a-heading), [out](../elsewhere.md), ![image](img/gone.png),",
		"a [link over",
		"two lines](docs/gone-too.md).",
		"`[in a span](docs/gone.md)`, `` [in a ` span](docs/gone.md) ``, [web](https://example.com/gone.md#x),",
		"[mail](mailto:a@example.com), [protocol-relative](//example.com/gone.md).",
		"",
		"```sh",
		"[in a fence](docs/gone.md)",
		"```",
		"",
		"~~~~",
		"```",
		"[in a fence with a shorter one inside](docs/gone.md)",
		"~~~~",
		"",
		"[ref]: docs/also-gone.md",
	}, "\n")
	setup := "# Setup\n\n## The checks\n\n## The checks\n\n```\n## Not a heading\n```\n\n" +
		"## `go_vet`, *fast* & [linked](x.md)!\n\nBack to the [README](../README.md#set-up).\n"
	repo := fstest.MapFS{
		"README.md":     {Data: []byte(readme)},
		"docs/setup.md": {Data: []byte(setup)},
		"docs/a b.md":   {Data: []byte("# Title\n")},
		"scripts/x.sh":  {Data: []byte("#!/bin/sh\n")},
	}
	got, err := docLinkProblems(repo, []string{"README.md", "docs/setup.md"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"README.md:8: docs/gone.md: docs/gone.md doesn't exist",
		"README.md:8: docs/setup.md#nowhere: docs/setup.md has no heading #nowhere",
		"README.md:8: #title-1: README.md has no heading #title-1",
		"README.md:9: docs/setup.md#not-a-heading: docs/setup.md has no heading #not-a-heading",
		"README.md:9: ../elsewhere.md: leads out of the repository",
		"README.md:9: img/gone.png: img/gone.png doesn't exist",
		"README.md:11: docs/gone-too.md: docs/gone-too.md doesn't exist",
		"README.md:24: docs/also-gone.md: docs/also-gone.md doesn't exist",
		"docs/setup.md:11: x.md: docs/x.md doesn't exist",
	}
	if !slices.Equal(got, want) {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	if _, err := docLinkProblems(repo, []string{"docs/gone.md"}); err == nil {
		t.Error("checking a doc that doesn't exist: no error")
	}
}

func TestHeadingSlugs(t *testing.T) {
	doc := strings.Join([]string{
		"# Claude Agent Orchestra",
		"## A spec run's setup",
		"### Herdr, tmux or cmux",
		"## Organs ##",
		"## Organs",
		"  ## Organs",
		"## `go_vet` and _emphasis_ in snake_case",
		"## [Linked](docs/x.md) <em>tagged</em>",
		"## C++ & Go  twice",
		"## Ünïcode 2.0",
		"#No space, no heading",
		"    ## Indented code, no heading",
		"```",
		"# A comment in a fence",
		"```",
	}, "\n")
	want := []string{
		"claude-agent-orchestra", "a-spec-runs-setup", "herdr-tmux-or-cmux", "organs", "organs-1", "organs-2",
		"go_vet-and-emphasis-in-snake_case", "linked-tagged", "c--go--twice", "ünïcode-20",
	}
	if got := headingSlugs(doc); !slices.Equal(got, want) {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// docLinkProblems checks the relative links in docs, Markdown files in repo: each must name a file repo has and,
// with an #anchor in a Markdown file, one of that file's headings. It says what is wrong with each link that
// doesn't, after the doc and line it is on.
func docLinkProblems(repo fs.FS, docs []string) ([]string, error) {
	anchors := map[string][]string{}
	var problems []string
	for _, doc := range docs {
		text, err := fs.ReadFile(repo, doc)
		if err != nil {
			return nil, err
		}
		for _, l := range relativeLinks(string(text)) {
			why, err := linkProblem(repo, doc, l.dest, anchors)
			if err != nil {
				return nil, err
			}
			if why != "" {
				problems = append(problems, fmt.Sprintf("%s:%d: %s: %s", doc, l.line, l.dest, why))
			}
		}
	}
	return problems, nil
}

// linkProblem says what is wrong with the link dest in doc, or "" if nothing is. anchors holds each Markdown file's
// heading anchors read so far, by path.
func linkProblem(repo fs.FS, doc, dest string, anchors map[string][]string) (string, error) {
	target, anchor, _ := strings.Cut(dest, "#")
	target, _, _ = strings.Cut(target, "?")
	target, err := url.PathUnescape(target)
	if err == nil {
		anchor, err = url.PathUnescape(anchor)
	}
	if err != nil {
		return err.Error(), nil
	}
	switch {
	case target == "":
		target = doc
	case strings.HasPrefix(target, "/"): // from the repository's root, as GitHub reads it
		target = path.Clean(strings.TrimLeft(target, "/"))
	default:
		target = path.Join(path.Dir(doc), target)
	}
	if !fs.ValidPath(target) {
		return "leads out of the repository", nil
	}
	info, err := fs.Stat(repo, target)
	if err != nil {
		return target + " doesn't exist", nil
	}
	if anchor == "" || info.IsDir() || !slices.Contains([]string{".md", ".markdown"}, strings.ToLower(path.Ext(target))) {
		return "", nil
	}
	if _, read := anchors[target]; !read {
		text, err := fs.ReadFile(repo, target)
		if err != nil {
			return "", err
		}
		anchors[target] = headingSlugs(string(text))
	}
	if !slices.Contains(anchors[target], anchor) {
		return fmt.Sprintf("%s has no heading #%s", target, anchor), nil
	}
	return "", nil
}

// docLink is a link's destination as written, and the line it is on.
type docLink struct {
	line int
	dest string
}

var (
	// inlineLink is the end of an inline link or image, ](dest) or ](dest "title"), dest perhaps in <>.
	inlineLink = regexp.MustCompile(`\]\(\s*(?:<([^<>\n]*)>|([^\s()<>]+))(?:\s+(?:"[^"\n]*"|'[^'\n]*'))?\s*\)`)
	// linkDefinition is a reference link's definition, [label]: dest.
	linkDefinition = regexp.MustCompile(`(?m)^ {0,3}\[[^\]\n]+\]:[ \t]*(?:<([^<>\n]*)>|(\S+))`)
	// urlScheme starts an absolute URL, such as https: or mailto:.
	urlScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)
)

// relativeLinks are the links in the Markdown doc that lead into the repository, in the order they appear: not
// those in code, nor absolute URLs.
func relativeLinks(doc string) []docLink {
	text := blankCodeSpans(blankFences(doc))
	type at struct {
		offset int
		docLink
	}
	var links []at
	for _, re := range []*regexp.Regexp{inlineLink, linkDefinition} {
		for _, m := range re.FindAllStringSubmatchIndex(text, -1) {
			var dest string
			if m[2] >= 0 { // in <>
				dest = text[m[2]:m[3]]
			} else {
				dest = text[m[4]:m[5]]
			}
			if dest == "" || urlScheme.MatchString(dest) || strings.HasPrefix(dest, "//") {
				continue
			}
			links = append(links, at{m[0], docLink{strings.Count(text[:m[0]], "\n") + 1, dest}})
		}
	}
	slices.SortFunc(links, func(a, b at) int { return a.offset - b.offset })
	var out []docLink
	for _, l := range links {
		out = append(out, l.docLink)
	}
	return out
}

var (
	// atxHeading is a heading line, its text without the #s that open and may close it.
	atxHeading = regexp.MustCompile(`^ {0,3}#{1,6}(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$`)
	// headingLink is a link or an image in a heading, which GitHub shows as its text.
	headingLink = regexp.MustCompile(`!?\[([^\]]*)\](?:\([^)]*\)|\[[^\]]*\])`)
	htmlTag     = regexp.MustCompile(`<[^>]*>`)
	// Underscores before or after a word, rather than in one, are emphasis.
	underscoresBefore = regexp.MustCompile(`(^|[^\pL\pN_])_+`)
	underscoresAfter  = regexp.MustCompile(`_+([^\pL\pN_]|$)`)
)

// headingSlugs are the anchors GitHub gives the Markdown doc's headings, in order. A heading's anchor is its text as
// GitHub shows it (a link's or image's text for the link, code spans' content, without HTML tags or emphasis),
// lowercase, with each character but letters, digits, marks, spaces, hyphens and underscores dropped and each space
// a hyphen; a repeated anchor then gets -1, -2 and so on.
func headingSlugs(doc string) []string {
	seen := map[string]int{}
	var slugs []string
	for line := range strings.SplitSeq(blankFences(doc), "\n") {
		m := atxHeading.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
		if m == nil {
			continue
		}
		text := htmlTag.ReplaceAllString(headingLink.ReplaceAllString(m[1], "$1"), "")
		parts := strings.Split(text, "`")
		for i := 0; i < len(parts); i += 2 { // outside code spans
			parts[i] = underscoresAfter.ReplaceAllString(underscoresBefore.ReplaceAllString(parts[i], "$1"), "$1")
		}
		slug := strings.Map(func(r rune) rune {
			switch {
			case r == ' ':
				return '-'
			case r == '-' || unicode.In(r, unicode.L, unicode.M, unicode.N, unicode.Pc):
				return r
			}
			return -1
		}, strings.ToLower(strings.TrimSpace(strings.Join(parts, ""))))
		if n := seen[slug]; n > 0 {
			seen[slug]++
			slug += "-" + strconv.Itoa(n)
		} else {
			seen[slug] = 1
		}
		slugs = append(slugs, slug)
	}
	return slugs
}

// codeFence opens or closes a fenced code block.
var codeFence = regexp.MustCompile("^[ \t]*(`{3,}|~{3,})")

// blankFences blanks out the Markdown doc's fenced code blocks, fences and all, keeping its lines. A block runs to
// a fence of its own character at least as long as the one that opened it, or to the end of doc.
func blankFences(doc string) string {
	lines := strings.SplitAfter(doc, "\n")
	open := ""
	for i, line := range lines {
		m := codeFence.FindStringSubmatch(line)
		rest := ""
		if m != nil {
			rest = line[len(m[0]):]
		}
		switch {
		case open == "" && (m == nil || m[1][0] == '`' && strings.Contains(rest, "`")):
			continue
		case open == "":
			open = m[1]
		case m != nil && m[1][0] == open[0] && len(m[1]) >= len(open) && strings.TrimSpace(rest) == "":
			open = ""
		}
		lines[i] = string(blankOut([]byte(line)))
	}
	return strings.Join(lines, "")
}

var (
	backticks      = regexp.MustCompile("`+")
	paragraphBreak = regexp.MustCompile(`\n[ \t]*\n`)
)

// blankCodeSpans blanks out text's code spans: each from a run of backticks to the next run of as many in the same
// paragraph. A run with none to close it is literal backticks.
func blankCodeSpans(text string) string {
	out := []byte(text)
	runs := backticks.FindAllStringIndex(text, -1)
	for i := 0; i < len(runs); i++ {
		for j := i + 1; j < len(runs); j++ {
			if paragraphBreak.MatchString(text[runs[i][1]:runs[j][0]]) {
				break
			}
			if runs[j][1]-runs[j][0] == runs[i][1]-runs[i][0] {
				blankOut(out[runs[i][0]:runs[j][1]])
				i = j
				break
			}
		}
	}
	return string(out)
}

// blankOut makes each byte of b but a newline a space, and returns b.
func blankOut(b []byte) []byte {
	for i, c := range b {
		if c != '\n' {
			b[i] = ' '
		}
	}
	return b
}
