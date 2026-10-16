package dispatch

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A glob in the files metadata that matches files that are there means them, and is kept too,
// anchored at each folder it matched in, however it is written: it holds the files another ticket
// adds there that it would match. A ticket listing internal/dispatch/*.go overlaps one listing a new
// internal/dispatch/cart.go, as it overlaps one listing a file that is there, but not one listing a
// new internal/dispatch/cart.ts, nor a file in a folder it matched nothing in.
func TestAGlobHoldsTheNewFilesItWouldMatch(t *testing.T) {
	dispatchGo := []string{"internal/dispatch/loop.go", "internal/dispatch/loop_test.go", "internal/dispatch/run.go"}
	for _, c := range []struct {
		entry        string
		files, globs []string
	}{
		{"internal/dispatch/*.go", dispatchGo, []string{"internal/dispatch/*.go"}},
		{"./internal//dispatch/*.go", dispatchGo, []string{"internal/dispatch/*.go"}},
		{"dispatch/*.go", dispatchGo, []string{"internal/dispatch/*.go"}},
		{"*.md", []string{"README.md", "docs/running.md"}, []string{"*.md", "docs/*.md"}},
		{"**/view.go", []string{"internal/tui/view/view.go"}, []string{"**/view.go"}},
		{"internal/**/view.go", []string{"internal/tui/view/view.go"}, []string{"internal/**/view.go"}},
		{"internal/*/", slices.Concat(dispatchGo, []string{"internal/tui/run.go", "internal/tui/view/view.go"}),
			[]string{"internal/*"}},
	} {
		fp := TicketFootprint(listing("a", c.entry), trackedNested, "")
		if !slices.Equal(fp.Files, c.files) || !slices.Equal(fp.Globs, c.globs) {
			t.Errorf("listing %s: files %q, globs %q, want files %q, globs %q", c.entry, fp.Files, fp.Globs, c.files, c.globs)
		}
	}

	for _, entry := range []string{"internal/dispatch/*.go", "dispatch/*.go"} {
		glob := TicketFootprint(listing("a", entry), trackedNested, "")
		for _, c := range []struct{ other, want string }{
			{"internal/dispatch/cart.go", "internal/dispatch/cart.go"},
			{"internal/dispatch/loop.go", "internal/dispatch/loop.go"},
			{"internal/dispatch/", "internal/dispatch/loop.go"},
			{"internal/dispatch/c*.go", "internal/dispatch/c*.go"}, // a glob matching none yet, which can match a file it does
			{"internal/dispatch/cart.ts", ""},
			{"internal/dispatch/*.ts", ""},
			{"internal/dispatch/web/a.go", ""},
			{"internal/tui/cart.go", ""},
			{"internal/cart.go", ""},
			{"cmd/dispatch/cart.go", ""},
		} {
			other := TicketFootprint(listing("b", c.other), trackedNested, "")
			if what := shared(glob, other, nil, nil); what != c.want {
				t.Errorf("%s and %s: shared %q, want %q", entry, c.other, what, c.want)
			}
			if what := shared(other, glob, nil, nil); what != c.want {
				t.Errorf("%s and %s: shared %q, want %q", c.other, entry, what, c.want)
			}
		}

		// A running ticket naming nothing whose worker adds a file the glob would match, and the reverse.
		added := []string{"internal/dispatch/cart.go"}
		if what := shared(glob, Footprint{}, added, nil); what != "internal/dispatch/cart.go" {
			t.Errorf("%s beside a worker adding internal/dispatch/cart.go: shared %q", entry, what)
		}
		if what := shared(Footprint{}, glob, added, nil); what != "" {
			t.Errorf("a ticket naming nothing beside %s: shared %q, want nothing", entry, what)
		}
	}
}

// A partial glob is anchored at every folder it matched in, and at none other; a folder's name with
// brackets (a SvelteKit route) is a part of the anchored glob as it is, matching itself.
func TestAGlobIsAnchoredWhereItMatched(t *testing.T) {
	tracked := []string{"cmd/dispatch/main.go", "internal/dispatch/loop.go", "internal/tui/run.go"}
	glob := TicketFootprint(listing("a", "dispatch/*.go"), tracked, "")
	if want := []string{"cmd/dispatch/*.go", "internal/dispatch/*.go"}; !slices.Equal(glob.Globs, want) {
		t.Errorf("listing dispatch/*.go: globs %q, want %q", glob.Globs, want)
	}
	for _, c := range []struct{ other, want string }{
		{"cmd/dispatch/flags.go", "cmd/dispatch/flags.go"},
		{"internal/dispatch/cart.go", "internal/dispatch/cart.go"},
		{"web/dispatch/cart.go", ""},
		{"internal/tui/cart.go", ""},
	} {
		if what := shared(TicketFootprint(listing("b", c.other), tracked, ""), glob, nil, nil); what != c.want {
			t.Errorf("%s and dispatch/*.go: shared %q, want %q", c.other, what, c.want)
		}
	}

	svelte := TicketFootprint(listing("a", "*.svelte"), trackedRoutes, "")
	if want := []string{"src/routes/[slug]/*.svelte", "src/routes/other/*.svelte"}; !slices.Equal(svelte.Globs, want) {
		t.Errorf("listing *.svelte: globs %q, want %q", svelte.Globs, want)
	}
	for _, c := range []struct{ other, want string }{
		{"src/routes/[slug]/+layout.svelte", "src/routes/[slug]/+layout.svelte"},
		{"src/routes/other/+layout.svelte", "src/routes/other/+layout.svelte"},
		{"src/routes/new/+page.svelte", ""},
		{"src/routes/[slug]/+page.ts", ""},
	} {
		if what := shared(TicketFootprint(listing("b", c.other), trackedRoutes, ""), svelte, nil, nil); what != c.want {
			t.Errorf("%s and *.svelte: shared %q, want %q", c.other, what, c.want)
		}
	}
}

// Two tickets listing a glob that matches files that are there are compared on those files one by
// one, as before: by function in a file where both name functions it mentions. A file one of them
// adds that the glob would match is shared.
func TestAGlobKeepsComparingItsFilesByFunction(t *testing.T) {
	tracked := []string{"README.md", "pkg/x.go"}
	in := sourcesIn(map[string]string{"pkg/x.go": "package pkg\n\nfunc Foo() {}\n\nfunc Bar() {}\n"})
	naming := func(id, fn string, files ...string) Footprint {
		tk := listing(id, files...)
		tk.Description = "Change " + fn + "()."
		return TicketFootprint(tk, tracked, "")
	}
	cases := []struct {
		name string
		a, b Footprint
		want string
	}{
		{"both list the glob", naming("a", "Foo", "pkg/*.go"), naming("b", "Bar", "pkg/*.go"), ""},
		{"one lists the file", naming("a", "Foo", "pkg/*.go"), naming("b", "Bar", "pkg/x.go"), ""},
		{"one lists the folder", naming("a", "Foo", "pkg/*.go"), naming("b", "Bar", "pkg/"), ""},
		{"one adds a file", naming("a", "Foo", "pkg/*.go"), naming("b", "Bar", "pkg/y.go"), "pkg/y.go"},
		{"one adds a file of another kind", naming("a", "Foo", "pkg/*.go"), naming("b", "Bar", "pkg/y.ts"), ""},
		{"the same function", naming("a", "Foo", "pkg/*.go"), naming("b", "Foo", "pkg/*.go"), "Foo"},
		{"a function the file doesn't mention", naming("a", "Foo", "pkg/*.go"), naming("b", "Baz", "pkg/*.go"), "pkg/x.go"},
	}
	for _, c := range cases {
		if what := shared(c.a, c.b, nil, in); what != c.want {
			t.Errorf("%s: shared %q, want %q", c.name, what, c.want)
		}
		if what := shared(c.b, c.a, nil, in); what != c.want {
			t.Errorf("%s, the other way: shared %q, want %q", c.name, what, c.want)
		}
	}
}

// orchestra plan reads the same footprints: a ticket listing internal/dispatch/*.go is ordered with
// one listing a new internal/dispatch/cart.go, which counts as small however large the files there
// are, but not with one listing a new internal/dispatch/cart.ts.
func TestPlanLinksAGlobToANewFileItWouldMatch(t *testing.T) {
	read := func(lines int) func(string) ([]byte, error) {
		return func(p string) ([]byte, error) {
			if !slices.Contains(trackedNested, p) {
				return nil, os.ErrNotExist
			}
			return []byte(strings.Repeat("\n", lines)), nil
		}
	}
	open := []Ticket{listing("a", "internal/dispatch/*.go"), listing("b", "internal/dispatch/cart.go"),
		listing("c", "internal/dispatch/cart.ts")}
	for i := range open {
		p := i + 1
		open[i].Priority = &p
	}
	for _, lines := range []int{50, 500} {
		got, want := linkList(PlanLinks(open, nil, trackedNested, "", read(lines))), []string{"a>b:internal/dispatch/cart.go"}
		if !slices.Equal(got, want) {
			t.Errorf("files of %d lines: links %q, want %q", lines, got, want)
		}
	}
}

// In a run, a ticket listing a new file a running ticket's glob would match waits for it, and the
// log says why; one listing a new file the glob wouldn't match runs beside it.
func TestARunKeepsANewFileOutOfAGlob(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 3
		h.commitFiles("internal/dispatch/loop.go")
		h.beads.add("A", "rework dispatch", 1)
		h.beads.add("B", "add the cart", 2)
		h.beads.add("C", "add the cart's page", 3)
		for id, files := range map[string]string{
			"A": "internal/dispatch/*.go", "B": "internal/dispatch/cart.go", "C": "internal/dispatch/cart.ts",
		} {
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
		h.worker("C", v.runs("C", finishes("c.txt")))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := v.of("B"); slices.Contains(got, "A") {
			t.Errorf("B ran beside A, whose glob matches the file B adds")
		}
		// C starts while A runs, before B, which waits for A.
		if got, want := h.sink.dispatched(), []string{"A", "C", "B"}; !slices.Equal(got, want) {
			t.Errorf("dispatched %q, want %q: C, whose file A's glob doesn't match, waited for A", got, want)
		}
		log := h.logged()
		for _, want := range []string{
			"A footprint: internal/dispatch/loop.go, internal/dispatch/*.go\n",
			"skipping B: touches internal/dispatch/cart.go, like running A",
		} {
			if !strings.Contains(log, want) {
				t.Errorf("the log lacks %q:\n%s", want, log)
			}
		}
	})
}
