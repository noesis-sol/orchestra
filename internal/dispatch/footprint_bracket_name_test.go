package dispatch

import (
	"encoding/json"
	"slices"
	"testing"
)

// trackedRoutes are the files of a Next.js and a SvelteKit project, whose routes are named with
// brackets: [id].tsx is a file of that name, which a glob's class would take for i.tsx or d.tsx.
var trackedRoutes = []string{
	"README.md", "pages/[id].tsx", "pages/i.tsx", "pages/x.tsx",
	"src/routes/[slug]/+page.svelte", "src/routes/other/+page.svelte",
}

// A files metadata entry with a [ is a glob, whose part also matches the name written as it is: a
// ticket listing pages/[id].tsx overlaps one listing that file, one predicted to change it and a worker
// editing it, whether the repository's files are known (the file there or a new one), empty or unknown.
// It still matches what its class does (pages/i.tsx), and nothing else.
func TestAListedNameWithABracketMeansThatFile(t *testing.T) {
	t.Parallel()
	for _, tracked := range [][]string{trackedRoutes, trackedHere, {}, nil} {
		route := TicketFootprint(listing("a", "pages/[id].tsx"), tracked, "")
		if slices.Equal(tracked, trackedRoutes) {
			want := []string{"pages/[id].tsx", "pages/i.tsx"}
			if !slices.Equal(route.Files, want) || !slices.Equal(route.Globs, []string{"pages/[id].tsx"}) {
				t.Errorf("listing pages/[id].tsx (files %v): files %q, globs %q, want files %q beside the glob", tracked,
					route.Files, route.Globs, want)
			}
		} else if !slices.Equal(route.Globs, []string{"pages/[id].tsx"}) || len(route.Files) > 0 {
			t.Errorf("listing pages/[id].tsx (files %v): files %q, globs %q, want the glob alone", tracked, route.Files,
				route.Globs)
		}

		listed := TicketFootprint(listing("b", "pages/[id].tsx"), tracked, "")
		for _, fp := range []Footprint{route, listed} {
			if what := shared(fp, listed, nil, nil); what != "pages/[id].tsx" {
				t.Errorf("two tickets listing pages/[id].tsx (files %v): shared %q", tracked, what)
			}
		}
		for _, c := range []struct{ edited, want string }{
			{"pages/[id].tsx", "pages/[id].tsx"},
			{"pages/i.tsx", "pages/i.tsx"},
			{"pages/x.tsx", ""},
			{"pages/[id].ts", ""},
			{"app/pages/[id].tsx", ""},
		} {
			if what := shared(route, Footprint{}, []string{c.edited}, nil); what != c.want {
				t.Errorf("pages/[id].tsx beside a worker editing %s (files %v): shared %q, want %q", c.edited, tracked,
					what, c.want)
			}
		}
		other := TicketFootprint(listing("b", "pages/[id].ts"), tracked, "")
		if what := shared(route, other, nil, nil); what != "" {
			t.Errorf("pages/[id].tsx beside pages/[id].ts (files %v): shared %q, want nothing", tracked, what)
		}
	}

	// A ticket predicted to change the file, where the prediction counts: the file there, or the files
	// unknown.
	predicted := Ticket{ID: "b", Title: "Say hello", Metadata: json.RawMessage(`{"predicted_files": "pages/[id].tsx"}`)}
	for _, tracked := range [][]string{trackedRoutes, nil} {
		route := TicketFootprint(listing("a", "pages/[id].tsx"), tracked, "")
		fp := TicketFootprint(predicted, tracked, "")
		if what := shared(route, fp, nil, nil); what != "pages/[id].tsx" {
			t.Errorf("pages/[id].tsx beside a prediction of it (files %v, %v): shared %q", tracked, fp, what)
		}
		if what := shared(fp, route, nil, nil); what != "pages/[id].tsx" {
			t.Errorf("a prediction of pages/[id].tsx beside it (files %v, %v): shared %q", tracked, fp, what)
		}
	}
}

// A folder with a [ in its files metadata entry (src/routes/[slug]/, a SvelteKit route) takes in the
// files under it: the repository's, with its files known, and any another ticket names or a worker
// edits, with them empty or unknown. A folder beside it is no part of it.
func TestAListedFolderWithABracketTakesInItsFiles(t *testing.T) {
	t.Parallel()
	for _, tracked := range [][]string{trackedRoutes, {}, nil} {
		route := TicketFootprint(listing("a", "src/routes/[slug]/"), tracked, "")
		if slices.Equal(tracked, trackedRoutes) {
			if want := []string{"src/routes/[slug]/+page.svelte"}; !slices.Equal(route.Files, want) {
				t.Errorf("listing src/routes/[slug]/ (files %v): files %q, want %q", tracked, route.Files, want)
			}
		} else if !slices.Equal(route.Globs, []string{"src/routes/[slug]"}) {
			t.Errorf("listing src/routes/[slug]/ (files %v): globs %q, want [src/routes/[slug]]", tracked, route.Globs)
		}

		for _, c := range []struct {
			edited string
			want   bool
		}{
			{"src/routes/[slug]/+page.svelte", true},
			{"src/routes/other/+page.svelte", false},
			{"src/routes/[slug].svelte", false},
		} {
			if what := shared(route, Footprint{}, []string{c.edited}, nil); (what != "") != c.want {
				t.Errorf("src/routes/[slug]/ beside a worker editing %s (files %v): shared %q", c.edited, tracked, what)
			}
		}
		file := TicketFootprint(listing("b", "src/routes/[slug]/+page.svelte"), tracked, "")
		if what := shared(route, file, nil, nil); what == "" {
			t.Errorf("src/routes/[slug]/ beside src/routes/[slug]/+page.svelte (files %v): shared nothing", tracked)
		}
		if len(tracked) > 0 {
			continue
		}
		// A file a ticket adds under it, the files unknown or empty.
		added := TicketFootprint(listing("b", "src/routes/[slug]/+page.ts"), tracked, "")
		if what := shared(added, route, nil, nil); what == "" {
			t.Errorf("a new src/routes/[slug]/+page.ts beside src/routes/[slug]/ (files %v): shared nothing", tracked)
		}
	}
}

// orchestra plan reads the same footprints: a ticket listing pages/[id].tsx is ordered with one listing
// that file, not with one listing pages/x.tsx.
func TestPlanLinksAListedNameWithABracketToThatFile(t *testing.T) {
	t.Parallel()
	for _, tracked := range [][]string{trackedRoutes, {}, nil} {
		open := []Ticket{listing("a", "pages/[id].tsx"), listing("b", "./pages/[id].tsx"), listing("c", "pages/x.tsx")}
		for i, p := range []int{1, 2, 3} {
			open[i].Priority = &p
		}
		got := linkList(PlanLinks(open, nil, tracked, "", sourceOf(nil), nil, nil))
		if want := []string{"a>b:pages/[id].tsx"}; !slices.Equal(got, want) {
			t.Errorf("files %v: links %q, want %q", tracked, got, want)
		}
	}
}

// partMeet compares a part of a glob with a file's or a folder's: the glob's part matches the names
// path.Match takes and the name written as it is, the file's its name alone, which a class or a * in
// it doesn't widen.
func TestAPartOfAGlobMeetsTheNameAsWritten(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		x, y part
		want bool
	}{
		{part{"[id].tsx", true}, part{"[id].tsx", false}, true},
		{part{"[id].tsx", true}, part{"i.tsx", false}, true},
		{part{"[id].tsx", true}, part{"x.tsx", false}, false},
		{part{`\[id].tsx`, true}, part{"[id].tsx", false}, true},
		{part{`\[id].tsx`, true}, part{`\[id].tsx`, false}, true},
		{part{`a\b`, true}, part{"ab", false}, true},
		{part{`a\b`, true}, part{`a\b`, false}, true},
		{part{"[a", true}, part{"[a", false}, true}, // malformed, matching the name as written alone
		{part{"[a", true}, part{"a", false}, false},
		{part{"[id].tsx", false}, part{"[id].tsx", false}, true},
		{part{"[id].tsx", false}, part{"i.tsx", false}, false},
		{part{"*.ts", false}, part{"cart.ts", false}, false},
		{part{"*.ts", false}, part{"*.ts", true}, true},
		{part{"*.ts", false}, part{"c*.ts", true}, false},
	} {
		if got := partMeet(c.x, c.y); got != c.want {
			t.Errorf("partMeet(%v, %v) = %v, want %v", c.x, c.y, got, c.want)
		}
		if got := partMeet(c.y, c.x); got != c.want {
			t.Errorf("partMeet(%v, %v) = %v, want %v", c.y, c.x, got, c.want)
		}
	}
}
