package dispatch

import (
	"context"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A ticket of a project built from a spec may list a glob in a folder that doesn't exist yet. It
// matches none of the repository's files, so it is kept as itself, cleaned the same way whether the
// files are known, empty or unknown, and holds the files and folders other tickets name that it would
// match from the repository's root, as a name kept for a new file is: the two overlap. A file or folder
// it can't match is no part of it, nor is a glob matching other files.
func TestAGlobInANewFolderHoldsTheFilesItWouldMatch(t *testing.T) {
	t.Parallel()
	for _, tracked := range [][]string{trackedHere, {}, nil} {
		for _, entry := range []string{"web/src/*.ts", "./web/src/*.ts", "web//src/*.ts", "web/lib/../src/*.ts"} {
			fp := TicketFootprint(listing("a", entry), tracked, "")
			if !slices.Equal(fp.Globs, []string{"web/src/*.ts"}) || len(fp.Files) > 0 {
				t.Errorf("listing %s (files %v): globs %q, files %q, want the glob web/src/*.ts alone", entry, tracked, fp.Globs, fp.Files)
			}
		}

		glob := TicketFootprint(listing("a", "web/src/*.ts"), tracked, "")
		for _, c := range []struct{ other, want string }{
			{"web/src/cart.ts", "web/src/cart.ts"},
			{"web/src/", "web/src/"},
			{"web/", "web/"},
			{"web/src/*.ts", "web/src/*.ts"},
			{"web/lib/a.ts", ""},
			{"web/src/cart.go", ""},
			{"web/src/ui/button.ts", ""},
			{"web/src/ui/", ""},
			{"web/srcx/a.ts", ""},
			{"app/web/src/a.ts", ""},
			{"internal/dispatch/", ""},
			{"web/src/*.go", ""},
			{"web/src/*.tsx", ""},
			{"web/lib/*.ts", ""},
			{"src/*.ts", ""},
		} {
			other := TicketFootprint(listing("b", c.other), tracked, "")
			if what := shared(glob, other, nil, nil); what != c.want {
				t.Errorf("web/src/*.ts and %s (files %v): shared %q, want %q", c.other, tracked, what, c.want)
			}
			if what := shared(other, glob, nil, nil); what != c.want {
				t.Errorf("%s and web/src/*.ts (files %v): shared %q, want %q", c.other, tracked, what, c.want)
			}
		}

		// Two globs that can match the same file overlap, whichever way round, on the first's glob.
		for _, other := range []string{"web/**/*.ts", "web/src/c*.ts", "web/*/cart.ts", "**/src/*.ts", "web/src/*", "web/src/[a-c]*.ts"} {
			fp := TicketFootprint(listing("b", other), tracked, "")
			if what := shared(glob, fp, nil, nil); what != "web/src/*.ts" {
				t.Errorf("web/src/*.ts and %s (files %v): shared %q, want web/src/*.ts", other, tracked, what)
			}
			if what := shared(fp, glob, nil, nil); what != fp.Globs[0] {
				t.Errorf("%s and web/src/*.ts (files %v): shared %q, want %s", other, tracked, what, fp.Globs[0])
			}
		}

		// A running ticket listing the glob, or naming nothing with its worker editing a file it matches.
		if what := shared(glob, Footprint{}, []string{"web/src/cart.ts"}, nil); what != "web/src/cart.ts" {
			t.Errorf("web/src/*.ts beside a worker editing web/src/cart.ts (files %v): shared %q", tracked, what)
		}
		if what := shared(Footprint{}, glob, []string{"web/src/cart.ts"}, nil); what != "" {
			t.Errorf("a ticket naming nothing beside web/src/*.ts (files %v): shared %q, want nothing", tracked, what)
		}
	}

	// A glob that matches none of the files in a folder that exists is kept too: a ticket adding a file
	// it matches overlaps it.
	glob := TicketFootprint(listing("a", "internal/dispatch/*.rs"), trackedHere, "")
	if !slices.Equal(glob.Globs, []string{"internal/dispatch/*.rs"}) {
		t.Errorf("listing internal/dispatch/*.rs: globs %q", glob.Globs)
	}
	added := TicketFootprint(listing("b", "internal/dispatch/cart.rs"), trackedHere, "")
	if what := shared(added, glob, nil, nil); what != "internal/dispatch/cart.rs" {
		t.Errorf("a new internal/dispatch/cart.rs beside internal/dispatch/*.rs: shared %q", what)
	}
}

// partMeet compares parts of globs: a part that is no pattern stands for one name, escapes undone,
// and two patterns meet when their literal beginnings and ends allow a name both match, or the name
// one of them is written as.
func TestPartsOfGlobsMeet(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		x, y string
		want bool
	}{
		{"cart.ts", "cart.ts", true},
		{"cart.ts", "cart.go", false},
		{"*.ts", "cart.ts", true},
		{"*.ts", "cart.go", false},
		{`\[id].tsx`, `\[id].tsx`, true}, // a file of the name, escaped
		{`\[id].tsx`, "*.tsx", true},
		{`\[id].tsx`, "[id].tsx", true}, // a class, i.tsx or d.tsx, and the name as written
		{`\[id].tsx`, "[ix].tsx", false},
		{"[id].tsx", "i.tsx", true},
		{"*.ts", "c*.ts", true},
		{"a*.ts", "*b.ts", true},
		{"*.ts", "*.go", false},
		{"*.ts", "*.tsx", false},
		{"a*", "b*", false},
		{`*\.ts`, "*.ts", true},
		{"[a", "a", false}, // malformed, matching only [a, as written
		{`a\`, "a", false},
	} {
		if got := partMeet(part{c.x, true}, part{c.y, true}); got != c.want {
			t.Errorf("partMeet(%q, %q) = %v, want %v", c.x, c.y, got, c.want)
		}
		if got := partMeet(part{c.y, true}, part{c.x, true}); got != c.want {
			t.Errorf("partMeet(%q, %q) = %v, want %v", c.y, c.x, got, c.want)
		}
	}
}

// orchestra plan reads the same footprints: a ticket listing a glob in a new folder is ordered with
// one listing a file it would match, not with one listing a file elsewhere.
func TestPlanLinksAGlobInANewFolderToTheFilesItWouldMatch(t *testing.T) {
	t.Parallel()
	for _, tracked := range [][]string{trackedHere, {}, nil} {
		open := []Ticket{listing("a", "web/src/*.ts"), listing("b", "web/src/cart.ts"), listing("c", "web/lib/a.ts")}
		for i, p := range []int{1, 2, 3} {
			open[i].Priority = &p
		}
		got := linkList(PlanLinks(open, nil, tracked, "", sourceOf(nil), nil, nil))
		if want := []string{"a>b:web/src/cart.ts"}; !slices.Equal(got, want) {
			t.Errorf("files %v: links %q, want %q", tracked, got, want)
		}
	}
}

// In a run, a ticket listing a file a running ticket's glob would match waits for it, and the log
// says why.
func TestARunKeepsAFileOutOfAGlobInANewFolder(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "lay out the pages", 1)
		h.beads.add("B", "add the cart", 2)
		for id, files := range map[string]string{"A": "web/src/*.ts", "B": "web/src/cart.ts"} {
			if err := h.beads.SetMetadata(context.Background(), id, FilesKey, files); err != nil {
				t.Fatal(err)
			}
		}
		var v overlap
		h.worker("A", v.runs("A", func(w *fakeWorker) AgentState {
			time.Sleep(3 * readyPoll) // a few polls, with a slot free for B
			return finishes("a.txt")(w)
		}))
		h.worker("B", v.runs("B", finishes("b.txt")))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := v.of("B"); slices.Contains(got, "A") {
			t.Errorf("B ran beside A, whose glob matches the file B lists")
		}
		log := h.logged()
		for _, want := range []string{
			"A footprint: web/src/*.ts\n",
			"skipping B: touches web/src/cart.ts, like running A",
		} {
			if !strings.Contains(log, want) {
				t.Errorf("the log lacks %q:\n%s", want, log)
			}
		}
	})
}
