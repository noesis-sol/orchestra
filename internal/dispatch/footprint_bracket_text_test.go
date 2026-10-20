package dispatch

import (
	"slices"
	"testing"
)

// A ticket whose text names pages/[id].tsx, a route of a Next.js project, names that file, whether
// the repository's files are known (the file there, or a new one in a folder that is) or not: it
// overlaps a ticket listing it and a worker editing it, and nothing beside it (pages/x.tsx), as the
// glob a listed name is would. It was read as pages/, id and .tsx, which named nothing of it.
func TestATicketNamingAPathWithABracketMeansThatFile(t *testing.T) {
	t.Parallel()
	for _, tracked := range [][]string{trackedRoutes, {"pages/x.tsx"}, nil} {
		text := TicketFootprint(Ticket{ID: "a", Title: "Fix pages/[id].tsx."}, tracked, "")
		if want := []string{"pages/[id].tsx"}; !slices.Equal(text.Files, want) || len(text.Funcs) > 0 {
			t.Errorf("files %v: footprint %q, want the files %q alone", tracked, text, want)
		}
		listed := TicketFootprint(listing("b", "pages/[id].tsx"), tracked, "")
		if what := shared(text, listed, nil, nil); what != "pages/[id].tsx" {
			t.Errorf("files %v: beside a ticket listing pages/[id].tsx (%q), shared %q", tracked, listed, what)
		}
		if what := shared(listed, text, nil, nil); what != "pages/[id].tsx" {
			t.Errorf("files %v: a ticket listing pages/[id].tsx beside it, shared %q", tracked, what)
		}
		for _, c := range []struct{ edited, want string }{
			{"pages/[id].tsx", "pages/[id].tsx"},
			{"pages/i.tsx", ""},
			{"pages/x.tsx", ""},
			{"app/pages/[id].tsx", ""},
		} {
			if what := shared(text, Footprint{}, []string{c.edited}, nil); what != c.want {
				t.Errorf("files %v: beside a worker editing %s, shared %q, want %q", tracked, c.edited, what, c.want)
			}
		}
		other := TicketFootprint(listing("b", "pages/x.tsx"), tracked, "")
		if what := shared(text, other, nil, nil); what != "" {
			t.Errorf("files %v: beside a ticket listing pages/x.tsx, shared %q, want nothing", tracked, what)
		}
	}
}

// Brackets inside a word are part of a path, single or double, around a name with dots, = or -; a
// Markdown link or task list item's start no part of one. A word with brackets that names nothing is
// read in its parts without them, as before: an index expression names the Type.method it holds.
func TestPathTokensHoldBracketsInsideAWord(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		text         string
		files, funcs []string
	}{
		{"Fix pages/[...slug].tsx and app/[[...slug]]/page.tsx",
			[]string{"app/[[...slug]]/page.tsx", "pages/[...slug].tsx"}, nil},
		{"Fix src/routes/[page=fruit]/[a]-[b].ts", []string{"src/routes/[page=fruit]/[a]-[b].ts"}, nil},
		{"See [README.md](/README.md) and [docs/x.md](docs/x.md)", []string{"README.md", "docs/x.md"}, nil},
		{"- [x] fix run.go", []string{"run.go"}, nil},
		{"Check `files[i]` and Footprint.Files[0] in loop.go[1]", []string{"loop.go"}, []string{"Footprint.Files"}},
		{"Map[K].Get, x[1:2] and a[b c]", nil, nil},
		{"Not /pages/[id].tsx nor https://example.com/pages/[id].tsx", nil, nil},
	} {
		fp := TicketFootprint(Ticket{Description: c.text}, nil, "")
		if !slices.Equal(fp.Files, c.files) || !slices.Equal(fp.Funcs, c.funcs) {
			t.Errorf("%q: files %q, functions %q; want %q, %q", c.text, fp.Files, fp.Funcs, c.files, c.funcs)
		}
	}
}

// An extension alone names no file, with the repository's files known or not: "the .tsx files" says
// what kind of file a ticket works on, not which. Without the files, .tsx was kept as a file of that
// name, overlapping every other ticket saying .tsx and none naming a .tsx file.
func TestAnExtensionAloneNamesNoFile(t *testing.T) {
	t.Parallel()
	for _, tracked := range [][]string{trackedRoutes, nil} {
		for _, text := range []string{"Move the .tsx files to .ts", "Rename ..tsx and .TSX"} {
			if fp := TicketFootprint(Ticket{Description: text}, tracked, ""); !fp.Empty() {
				t.Errorf("%q (files %v): footprint %q, want empty", text, tracked, fp)
			}
		}
		// A name before the extension, even a dotfile's, still counts.
		fp := TicketFootprint(Ticket{Description: "Fix x.tsx and .golangci.yml"}, nil, "")
		if want := []string{".golangci.yml", "x.tsx"}; !slices.Equal(fp.Files, want) {
			t.Errorf("files unknown: files %q, want %q", fp.Files, want)
		}
	}
}

// orchestra plan reads the same footprints: a ticket naming pages/[id].tsx in its text is ordered with
// one listing that file.
func TestPlanLinksATicketNamingAPathWithABracket(t *testing.T) {
	t.Parallel()
	for _, tracked := range [][]string{trackedRoutes, nil} {
		open := []Ticket{{ID: "a", Title: "Fix pages/[id].tsx"}, listing("b", "pages/[id].tsx"), listing("c", "pages/x.tsx")}
		for i, p := range []int{1, 2, 3} {
			open[i].Priority = &p
		}
		got := linkList(PlanLinks(open, nil, tracked, "", sourceOf(nil), nil, nil))
		if want := []string{"a>b:pages/[id].tsx"}; !slices.Equal(got, want) {
			t.Errorf("files %v: links %q, want %q", tracked, got, want)
		}
	}
}
