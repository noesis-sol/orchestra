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

// A folder that exists, listed in the files metadata however it is written, is kept beside the
// tracked files under it, and holds the files another ticket adds in it: a ticket listing
// internal/dispatch/ overlaps one listing internal/dispatch/cart.go, a file nobody has added yet,
// as it overlaps one listing a file that is there. A folder of a name it begins with is no part of
// it.
func TestAListedFolderHoldsTheNewFilesUnderIt(t *testing.T) {
	want := []string{"internal/dispatch/", "internal/dispatch/loop.go", "internal/dispatch/loop_test.go",
		"internal/dispatch/run.go"}
	entries := []string{"internal/dispatch/", "internal/dispatch", "./internal/dispatch/.", "dispatch/", "dispatch"}
	for _, entry := range entries {
		if fp := TicketFootprint(listing("a", entry), trackedNested, ""); !slices.Equal(fp.Files, want) {
			t.Errorf("listing %s: files %q, want %q", entry, fp.Files, want)
		}
	}

	folder := TicketFootprint(listing("a", "internal/dispatch/"), trackedNested, "")
	for _, c := range []struct{ other, want string }{
		{"internal/dispatch/cart.go", "internal/dispatch/cart.go"},
		{"internal/dispatch/cart/", "internal/dispatch/cart/"},
		{"internal/dispatch/cart/a.go", "internal/dispatch/cart/a.go"},
		{"internal/dispatch/loop.go", "internal/dispatch/loop.go"},
		{"internal/", "internal/dispatch/loop.go"},
		{"internal/dispatchx/a.go", ""},
		{"internal/cart.go", ""},
		{"internal/tui/", ""},
	} {
		other := TicketFootprint(listing("b", c.other), trackedNested, "")
		if what := shared(folder, other, nil, nil); what != c.want {
			t.Errorf("internal/dispatch/ and %s: shared %q, want %q", c.other, what, c.want)
		}
		if what := shared(other, folder, nil, nil); what != c.want {
			t.Errorf("%s and internal/dispatch/: shared %q, want %q", c.other, what, c.want)
		}
	}

	// A running ticket naming nothing whose worker adds a file in the folder, and the reverse.
	added := []string{"internal/dispatch/cart.go"}
	if what := shared(folder, Footprint{}, added, nil); what != "internal/dispatch/cart.go" {
		t.Errorf("internal/dispatch/ beside a worker adding internal/dispatch/cart.go: shared %q", what)
	}
	if what := shared(Footprint{}, folder, added, nil); what != "" {
		t.Errorf("a ticket naming nothing beside internal/dispatch/: shared %q, want nothing", what)
	}
}

// Two tickets listing a folder that exists are compared on its files one by one, as before: by
// function in a file where both name functions it mentions, so tickets naming different functions
// in the only file there run side by side. A file one of them adds in it is shared.
func TestAListedFolderKeepsComparingItsFilesByFunction(t *testing.T) {
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
		{"both list the folder", naming("a", "Foo", "pkg/"), naming("b", "Bar", "pkg/"), ""},
		{"one lists the file", naming("a", "Foo", "pkg/"), naming("b", "Bar", "pkg/x.go"), ""},
		{"one adds a file", naming("a", "Foo", "pkg/"), naming("b", "Bar", "pkg/", "pkg/y.go"), "pkg/y.go"},
		{"the same function", naming("a", "Foo", "pkg/"), naming("b", "Foo", "pkg/"), "Foo"},
		{"a function the file doesn't mention", naming("a", "Foo", "pkg/"), naming("b", "Baz", "pkg/"), "pkg/x.go"},
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

// orchestra plan reads the same footprints: a ticket listing a folder that exists is ordered with
// one listing a file added in it, which counts as small, but not, for the folder alone, with
// another listing it when every file there is large, nor when both name different functions in
// the small file there.
func TestPlanLinksAListedFolderToANewFileInIt(t *testing.T) {
	read := func(lines int) func(string) ([]byte, error) {
		return func(p string) ([]byte, error) {
			if !slices.Contains(trackedNested, p) {
				return nil, os.ErrNotExist
			}
			return []byte(strings.Repeat("\n", lines)), nil
		}
	}
	prioritise := func(open []Ticket) []Ticket {
		for i := range open {
			p := i + 1
			open[i].Priority = &p
		}
		return open
	}

	open := prioritise([]Ticket{listing("a", "internal/dispatch/"), listing("b", "internal/dispatch/cart.go"),
		listing("c", "internal/dispatchx/a.go")})
	for _, lines := range []int{50, 500} {
		got, want := linkList(PlanLinks(open, nil, trackedNested, "", read(lines))), []string{"a>b:internal/dispatch/cart.go"}
		if !slices.Equal(got, want) {
			t.Errorf("files of %d lines: links %q, want %q", lines, got, want)
		}
	}

	both := prioritise([]Ticket{listing("a", "internal/dispatch/"), listing("b", "internal/dispatch/")})
	if got := PlanLinks(both, nil, trackedNested, "", read(500)); len(got) != 0 {
		t.Errorf("two tickets listing a folder of large files: links %q, want none", linkList(got))
	}
	small, want := linkList(PlanLinks(both, nil, trackedNested, "", read(50))), []string{"a>b:internal/dispatch/loop.go"}
	if !slices.Equal(small, want) {
		t.Errorf("two tickets listing a folder of small files: links %q, want %q", small, want)
	}

	byFunction := prioritise([]Ticket{listing("a", "pkg/"), listing("b", "pkg/")})
	byFunction[0].Description, byFunction[1].Description = "Change Foo().", "Change Bar()."
	source := sourceOf(map[string]string{"pkg/x.go": "package pkg\n\nfunc Foo() {}\n\nfunc Bar() {}\n"})
	if got := PlanLinks(byFunction, nil, []string{"pkg/x.go"}, "", source); len(got) != 0 {
		t.Errorf("two tickets listing a folder, naming different functions in its file: links %q, want none", linkList(got))
	}
}

// In a run, a ticket listing a file nobody has added yet in a folder a running ticket lists waits
// for it, and the log says why.
func TestARunKeepsANewFileOutOfAListedFolder(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.commitFiles("internal/dispatch/loop.go")
		h.beads.add("A", "rework dispatch", 1)
		h.beads.add("B", "add the cart", 2)
		for id, files := range map[string]string{"A": "internal/dispatch/", "B": "internal/dispatch/cart.go"} {
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
			t.Errorf("B ran beside A, which lists the folder B adds a file in")
		}
		log := h.logged()
		for _, want := range []string{
			"A footprint: internal/dispatch/, internal/dispatch/loop.go\n",
			"skipping B: touches internal/dispatch/cart.go, like running A",
		} {
			if !strings.Contains(log, want) {
				t.Errorf("the log lacks %q:\n%s", want, log)
			}
		}
	})
}
