package dispatch

import (
	"slices"
	"testing"
)

// A ticket whose text names src/routes/[slug]/+page.svelte, a route of a SvelteKit project, names that
// file, whether the repository's files are known (the file there, or a new one in a folder that is) or
// not: it overlaps a ticket listing it and a worker editing it, and nothing beside it (the other
// route's +page.svelte, a page.svelte). It was read as src/routes/[slug] and page.svelte, which named
// nothing of it.
func TestATicketNamingAPathWithAPlusMeansThatFile(t *testing.T) {
	t.Parallel()
	const route = "src/routes/[slug]/+page.svelte"
	for _, tracked := range [][]string{trackedRoutes, {"src/routes/[slug]/x.ts"}, nil} {
		text := TicketFootprint(Ticket{ID: "a", Title: "Fix " + route + "."}, tracked, "")
		if want := []string{route}; !slices.Equal(text.Files, want) || len(text.Funcs) > 0 {
			t.Errorf("files %v: footprint %q, want the files %q alone", tracked, text, want)
		}
		listed := TicketFootprint(listing("b", route), tracked, "")
		if what := shared(text, listed, nil, nil); what != route {
			t.Errorf("files %v: beside a ticket listing %s (%q), shared %q", tracked, route, listed, what)
		}
		if what := shared(listed, text, nil, nil); what != route {
			t.Errorf("files %v: a ticket listing %s beside it, shared %q", tracked, route, what)
		}
		for _, c := range []struct{ edited, want string }{
			{route, route},
			{"src/routes/other/+page.svelte", ""},
			{"src/routes/[slug]/page.svelte", ""},
			{"page.svelte", ""},
		} {
			if what := shared(text, Footprint{}, []string{c.edited}, nil); what != c.want {
				t.Errorf("files %v: beside a worker editing %s, shared %q, want %q", tracked, c.edited, what, c.want)
			}
		}
		other := TicketFootprint(listing("b", "src/routes/other/+page.svelte"), tracked, "")
		if what := shared(text, other, nil, nil); what != "" {
			t.Errorf("files %v: beside a ticket listing the other route's +page.svelte, shared %q, want nothing",
				tracked, what)
		}
	}
}

// A + is part of a path at the start of a name, at a word's start or after a /, but not after a word it
// joins to another (a+b.go, x[0]+y.go, (a)+g.go: the + is left off) nor one with no name after it (C++,
// Go 1.26+). A word with a + that names nothing is read without it, as before: +loop.go, an added line,
// names loop.go when the repository has no +loop.go, and +Loop.merge the Type.method.
func TestPathTokensHoldAPlusStartingAName(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		text         string
		tracked      []string
		files, funcs []string
	}{
		{"Fix src/routes/[slug]/+page.svelte and +layout.ts", nil,
			[]string{"+layout.ts", "src/routes/[slug]/+page.svelte"}, nil},
		{"See **+server.js**, `+error.svelte` and [+page.ts](src/routes/+page.ts)", nil,
			[]string{"+error.svelte", "+page.ts", "+server.js", "src/routes/+page.ts"}, nil},
		{"Port C++ and Go 1.26+, a+b.go, x[0]+y.go, (a)+g.go and c++d.go", nil,
			[]string{"b.go", "d.go", "g.go", "y.go"}, nil},
		{"Not a/++b.go nor +.go", nil, []string{"b.go"}, nil},
		{"Fix +page.svelte", trackedRoutes,
			[]string{"src/routes/[slug]/+page.svelte", "src/routes/other/+page.svelte"}, nil},
		{"+loop.go and +Loop.merge", []string{"internal/dispatch/loop.go"},
			[]string{"internal/dispatch/loop.go"}, []string{"Loop.merge"}},
		{"Not /src/routes/+page.svelte nor https://example.com/+page.svelte", nil, nil, nil},
	} {
		fp := TicketFootprint(Ticket{Description: c.text}, c.tracked, "")
		if !slices.Equal(fp.Files, c.files) || !slices.Equal(fp.Funcs, c.funcs) {
			t.Errorf("%q (files %v): files %q, functions %q; want %q, %q", c.text, c.tracked, fp.Files, fp.Funcs,
				c.files, c.funcs)
		}
	}
}
