package dispatch

import (
	"slices"
	"testing"
)

// listing is a ticket whose files metadata lists files.
func listing(id string, files ...string) Ticket {
	meta := `{"files": [`
	for i, f := range files {
		if i > 0 {
			meta += ", "
		}
		meta += `"` + f + `"`
	}
	return Ticket{ID: id, Title: "Work on " + id, Metadata: []byte(meta + "]}")}
}

// A ticket of a project built from a spec may list a folder that doesn't exist yet. Written as a
// folder, it is kept as one, cleaned the same way whether the repository's files are known, empty
// or unknown, and holds the files under it that other tickets name: the two overlap. A folder of a
// name it begins with (web/srcx/) is no part of it, and a name written without a trailing slash is
// a file.
func TestANewFolderInFilesMetadataHoldsItsFiles(t *testing.T) {
	for _, tracked := range [][]string{trackedHere, {}, nil} {
		for _, entry := range []string{"web/src/", "./web/src/", "web//src/", "web/src/./", "web/lib/../src/", "web/src/ui/.."} {
			if fp := TicketFootprint(listing("a", entry), tracked, ""); !slices.Equal(fp.Files, []string{"web/src/"}) {
				t.Errorf("listing %s (files %v): files %q, want [web/src/]", entry, tracked, fp.Files)
			}
		}

		folder := TicketFootprint(listing("a", "web/src/"), tracked, "")
		for _, c := range []struct{ other, want string }{
			{"web/src/cart.ts", "web/src/cart.ts"},
			{"web/src/ui/button.ts", "web/src/ui/button.ts"},
			{"web/src/ui/", "web/src/ui/"},
			{"web/", "web/src/"},
			{"web/src/", "web/src/"},
			{"web/srcx/a.ts", ""},
			{"web/srcx/", ""},
			{"web/src", ""},
			{"web/cart.ts", ""},
		} {
			other := TicketFootprint(listing("b", c.other), tracked, "")
			if what := shared(folder, other, nil, nil); what != c.want {
				t.Errorf("web/src/ and %s (files %v): shared %q, want %q", c.other, tracked, what, c.want)
			}
			if what := shared(other, folder, nil, nil); what != c.want {
				t.Errorf("%s and web/src/ (files %v): shared %q, want %q", c.other, tracked, what, c.want)
			}
		}

		// A running ticket listing the folder, or naming nothing with its worker editing a file in it.
		if what := shared(folder, Footprint{}, []string{"web/src/cart.ts"}, nil); what != "web/src/cart.ts" {
			t.Errorf("web/src/ beside a worker editing web/src/cart.ts (files %v): shared %q", tracked, what)
		}
		if what := shared(Footprint{}, folder, []string{"web/src/cart.ts"}, nil); what != "" {
			t.Errorf("a ticket naming nothing beside web/src/ (files %v): shared %q, want nothing", tracked, what)
		}

		// Without a trailing slash, a name is a file, as before.
		if fp := TicketFootprint(listing("a", "web/src"), tracked, ""); !slices.Equal(fp.Files, []string{"web/src"}) {
			t.Errorf("listing web/src (files %v): files %q, want [web/src]", tracked, fp.Files)
		}
	}

	// A folder in a ticket's text, or among its predicted files, is no folder of the files metadata's.
	predicted := TicketFootprint(Ticket{Title: "Say hello", Metadata: []byte(`{"predicted_files": "web/src/"}`)}, nil, "")
	if !slices.Equal(predicted.Files, []string{"web/src"}) {
		t.Errorf("a predicted web/src/ with the files unknown: files %q, want [web/src]", predicted.Files)
	}

	// With the repository's files unknown, a folder that exists is kept as one too, holding its files.
	folder := TicketFootprint(listing("a", "internal/dispatch/"), nil, "")
	if !slices.Equal(folder.Files, []string{"internal/dispatch/"}) {
		t.Errorf("listing internal/dispatch/ with the files unknown: files %q", folder.Files)
	}
	file := TicketFootprint(Ticket{Title: "Fix internal/dispatch/loop.go"}, nil, "")
	if what := shared(file, folder, nil, nil); what != "internal/dispatch/loop.go" {
		t.Errorf("a file in a folder listed with the files unknown: shared %q", what)
	}
}

// orchestra plan reads the same footprints: a ticket listing a new folder is ordered with one
// listing a file under it, not with one in a folder of a name it begins with.
func TestPlanLinksANewFolderToTheFilesUnderIt(t *testing.T) {
	for _, tracked := range [][]string{trackedHere, {}, nil} {
		open := []Ticket{listing("a", "web/src/"), listing("b", "web/src/cart.ts"), listing("c", "web/srcx/a.ts")}
		for i, p := range []int{1, 2, 3} {
			open[i].Priority = &p
		}
		got := linkList(PlanLinks(open, nil, tracked, "", sourceOf(nil)))
		if want := []string{"a>b:web/src/cart.ts"}; !slices.Equal(got, want) {
			t.Errorf("files %v: links %q, want %q", tracked, got, want)
		}
	}
}
