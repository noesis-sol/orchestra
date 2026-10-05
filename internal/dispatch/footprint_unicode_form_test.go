package dispatch

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// résumé.md in its two Unicode forms: NFC, é one letter, as git on macOS lists names
// (core.precomposeunicode); and NFD, e and a combining accent (U+0301), as Finder and HFS+ give them.
const (
	resumeNFC = "docs/résumé.md"
	resumeNFD = "docs/résumé.md"
)

// A ticket naming a file in one Unicode form finds the file git lists in the other, and gets it as
// git lists it: named in full, by its base name, in the files metadata, or by the check command.
func TestTicketFootprintMatchesEitherUnicodeForm(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ tracked, named string }{
		{resumeNFC, resumeNFD},
		{resumeNFD, resumeNFC},
	} {
		tracked := append(slices.Clone(trackedHere), c.tracked)
		meta, err := json.Marshal(map[string][]string{FilesKey: {c.named}})
		if err != nil {
			t.Fatal(err)
		}
		for _, tk := range []Ticket{
			{Description: "Rewrite " + c.named + "."},
			{Description: "Shorten " + strings.TrimPrefix(c.named, "docs/")},
			{Title: "Say hello", Metadata: meta},
		} {
			if fp := TicketFootprint(tk, tracked, ""); !slices.Equal(fp.Files, []string{c.tracked}) {
				t.Errorf("%+q: files %+q, want %+q", tk.Description+string(tk.Metadata), fp.Files, c.tracked)
			}
		}
		// The check command names the file in the other form than the ticket.
		only := Ticket{Title: "Say hello", AcceptanceCriteria: c.tracked + " passes"}
		if fp := TicketFootprint(only, tracked, "sh "+c.named); !fp.Empty() {
			t.Errorf("a ticket naming only the check's file, as %+q: footprint %+q, want empty", c.tracked, fp)
		}
	}

	// A new file is given in NFC, whichever form names it, so two tickets naming it meet.
	for _, named := range []string{"docs/café.md", "docs/café.md"} {
		fp := TicketFootprint(Ticket{Description: "Add " + named}, append(slices.Clone(trackedHere), resumeNFD), "")
		if want := []string{"docs/café.md"}; !slices.Equal(fp.Files, want) {
			t.Errorf("a new file named %+q: files %+q, want %+q", named, fp.Files, want)
		}
	}

	// Two files whose names differ only in form, which git can track on Linux, are both meant.
	both := append(slices.Clone(trackedHere), resumeNFC, resumeNFD)
	fp := TicketFootprint(Ticket{Description: "Rewrite " + resumeNFC}, both, "")
	if want := []string{resumeNFD, resumeNFC}; !slices.Equal(fp.Files, want) {
		t.Errorf("both forms tracked: files %+q, want %+q", fp.Files, want)
	}
}

// A file a worker reports editing in the other Unicode form than git lists it in is still the file
// a ready ticket names: the ticket waits for the running one.
func TestEditsInEitherUnicodeFormOverlap(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ tracked, edited string }{
		{resumeNFC, resumeNFD},
		{resumeNFD, resumeNFC},
	} {
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.commitFiles(c.tracked, "internal/y.go")
			edits := &editsReporter{}
			h.reporter = edits
			h.cfg.Concurrency = 2
			h.beads.add("A", "first", 1) // names nothing
			var v overlap
			h.worker("A", v.runs("A", func(w *fakeWorker) AgentState {
				w.claim()
				edits.edit(w.wt, c.edited)
				w.beads.add("B", "second", 2)
				w.beads.describe("B", "Rewrite "+c.tracked+".")
				w.beads.add("C", "third", 3)
				w.beads.describe("C", "Change internal/y.go.")
				time.Sleep(3 * readyPoll) // C takes the free slot at the next poll; then a few more
				return finishes("a.txt")(w)
			}))
			h.worker("B", v.runs("B", finishes("b.txt")))
			h.worker("C", v.runs("C", finishes("c.txt")))
			o, code := h.run()
			if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
				t.Fatalf("%+q edited: exit %d, final %q\n%s", c.edited, code, o.Final(), h.sink.text())
			}
			if got := v.of("B"); slices.Contains(got, "A") {
				t.Errorf("%+q edited: B ran beside A, which edits the file it names", c.edited)
			}
			skip := fmt.Sprintf("skipping B: touches %s, like running A", c.tracked)
			if log := h.logged(); strings.Count(log, skip) != 1 {
				t.Errorf("%+q edited: the skip was not logged once:\n%s", c.edited, log)
			}
		})
	}
}
