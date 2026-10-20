package dispatch

import (
	"context"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

var trackedWithGuide = append(slices.Clone(trackedHere), "docs/guide.md")

// A ticket cites the documents its text names in no sentence asking to change them, and every file
// its text names when its files metadata lists the files it changes; a source file named in the text
// of a ticket without files metadata is one it changes.
func TestTicketFootprintCitesWhatItOnlyReads(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		text  string
		cited bool
	}{
		{"Tests over a second skip under testing.Short(), per docs/guide.md.", true},
		{"Reproduce per docs/guide.md:\n- run the race tests", true},
		{"Spike 1 (docs/guide.md, item 4) found, with SDK 0.3, that the help says it updates nothing.", true},
		{"The help says it disables docs/guide.md, and the hooks with it.", true},
		{"Change internal/a.go, per docs/guide.md.", true},
		{"Update the run's section as docs/guide.md says.", true},
		{"Update the tests (docs/guide.md, item 4).", true},
		{"Update docs/guide.md.", false},
		{"Add the result to docs/guide.md § 4.", false},
		{"Say the colours in docs/guide.md's Current paragraph.", false},
		{"- docs/guide.md: the run's section.", false},
		{"docs/guide.md and README.md updated.", false},
		{"Done. docs/guide.md or README.md says how.", false},
	} {
		fp := TicketFootprint(Ticket{Description: c.text}, trackedWithGuide, "")
		if !slices.Contains(fp.Files, "docs/guide.md") || fp.Listed {
			t.Errorf("%q: footprint %+v lacks docs/guide.md", c.text, fp)
		}
		if got := slices.Contains(fp.Cited, "docs/guide.md"); got != c.cited {
			t.Errorf("%q: docs/guide.md cited %v, want %v (%+v)", c.text, got, c.cited, fp)
		}
	}

	src := TicketFootprint(Ticket{Description: "As internal/dispatch/loop.go does, per docs/guide.md."}, trackedWithGuide, "")
	if want := []string{"docs/guide.md"}; !slices.Equal(src.Cited, want) {
		t.Errorf("a source file named in passing: cited %q, want %q", src.Cited, want)
	}

	listed := Ticket{Description: "Change internal/dispatch/loop.go and update docs/guide.md, as README.md says.",
		Metadata: []byte(`{"files": ["internal/tui/run.go", "README.md"]}`)}
	fp := TicketFootprint(listed, trackedWithGuide, "")
	if want := []string{"docs/guide.md", "internal/dispatch/loop.go"}; !slices.Equal(fp.Cited, want) || !fp.Listed {
		t.Errorf("a ticket with files metadata: cited %q (listed %v), want %q", fp.Cited, fp.Listed, want)
	}
	if want := []string{"README.md", "docs/guide.md", "internal/dispatch/loop.go", "internal/tui/run.go"}; !slices.Equal(fp.Files, want) {
		t.Errorf("a ticket with files metadata: files %q, want %q", fp.Files, want)
	}
}

// A file two footprints share keeps them apart only when one of them is to change it: one that lists it,
// names it without only citing it, or names a function in it (but in a document, which mentions functions
// without holding them, and but for a ticket whose files metadata lists the files it changes), or one
// whose worker edited it.
func TestSharedFileOnlyWhenOneSideChangesIt(t *testing.T) {
	t.Parallel()
	const doc, src = "docs/guide.md", "internal/dispatch/loop.go"
	cites := func(f string) Footprint { return Footprint{Files: []string{f}, Cited: []string{f}} }
	changes := func(f string) Footprint { return Footprint{Files: []string{f}} }
	names := Footprint{Funcs: []string{"refreshBranch"}}
	listing := Footprint{Files: []string{"internal/tui/run.go"}, Funcs: []string{"refreshBranch"}, Listed: true}
	in := func(file, fn string) bool { return fn == "refreshBranch" }
	for _, c := range []struct {
		name   string
		f, r   Footprint
		edited []string
		want   string
	}{
		{"both cite it", cites(doc), cites(doc), nil, ""},
		{"the ready one changes it", changes(doc), cites(doc), nil, doc},
		{"the running one changes it", cites(doc), changes(doc), nil, doc},
		{"the running one's worker edited it", cites(doc), cites(doc), []string{doc}, doc},
		{"a function a cited document mentions", names, cites(doc), nil, ""},
		{"a function a document it changes mentions", names, changes(doc), nil, doc},
		{"a function a cited source file holds", names, cites(src), nil, src},
		{"a function a cited source file holds, from a ticket listing its files", listing, cites(src), nil, ""},
	} {
		if got := shared(c.f, c.r, c.edited, in); got != c.want {
			t.Errorf("%s: shared %q, want %q", c.name, got, c.want)
		}
	}
}

// Two tickets that only cite a document run side by side. A ticket whose files metadata lists it waits
// for a running ticket whose worker edited it, and so does one citing it.
func TestTicketsCitingADocumentRunSideBySide(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name         string
		listed, edit bool // B's files metadata lists the document; A's worker edits it
		beside       bool
	}{
		{"both cite it", false, false, true},
		{"B lists it, A's worker edited it", true, true, false},
		{"both cite it, A's worker edited it", false, true, false},
	} {
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.commitFiles("docs/guide.md", "internal/a.go", "internal/b.go")
			edits := &editsReporter{}
			h.reporter = edits
			h.cfg.Concurrency = 2
			h.beads.add("A", "first", 1)
			h.beads.describe("A", "Change internal/a.go, per docs/guide.md.")
			var v overlap
			h.worker("A", v.runs("A", func(w *fakeWorker) AgentState {
				w.claim()
				if c.edit {
					edits.edit(w.wt, "docs/guide.md")
				}
				w.beads.add("B", "second", 2)
				w.beads.describe("B", "Change internal/b.go, following docs/guide.md's conventions.")
				if c.listed {
					if err := w.beads.SetMetadata(context.Background(), "B", FilesKey, "internal/b.go,docs/guide.md"); err != nil {
						t.Error(err)
					}
				}
				time.Sleep(3 * readyPoll) // B takes the free slot at the next poll, if it may; then a few more
				return finishes("a.txt")(w)
			}))
			h.worker("B", v.runs("B", finishes("b.txt")))
			o, code := h.run()
			if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
				t.Fatalf("%s: exit %d, final %q\n%s", c.name, code, o.Final(), h.sink.text())
			}
			if got := slices.Contains(v.of("B"), "A"); got != c.beside {
				t.Errorf("%s: B ran beside A: %v, want %v", c.name, got, c.beside)
			}
			log := h.logged()
			skip := "skipping B: touches docs/guide.md, like running A"
			if got := strings.Contains(log, skip); got == c.beside {
				t.Errorf("%s: B skipped for docs/guide.md: %v, want %v:\n%s", c.name, got, !c.beside, log)
			}
		})
	}
}
