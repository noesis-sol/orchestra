package dispatch

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// Two tickets whose only shared file is one git merges by union (the changelog, with merge=union)
// run side by side, and their workers both editing it are no likely conflict: git keeps both
// sides' lines. Without the union attribute they don't, and a shared file merged normally still
// keeps them apart.
func TestFilesMergedByUnionDontKeepTicketsApart(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name          string
		union, shared bool // the changelog merged by union; both tickets naming internal/y.go too
		beside        bool
		footprint     string
	}{
		{"merged by union", true, false, true, "A footprint: internal/a.go\n"},
		{"merged normally", false, false, false, "A footprint: CHANGELOG.md, internal/a.go\n"},
		{"merged by union beside a shared file", true, true, false, "A footprint: internal/a.go, internal/y.go\n"},
	} {
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.commitFiles("CHANGELOG.md", "internal/a.go", "internal/b.go", "internal/y.go")
			if c.union {
				h.mem.mergeByUnion("CHANGELOG.md")
			}
			edits := &editsReporter{}
			h.reporter = edits
			h.cfg.Concurrency = 2
			also := ""
			if c.shared {
				also = " and internal/y.go"
			}
			h.beads.add("A", "first", 1)
			h.beads.describe("A", "Change internal/a.go"+also+", with a line in CHANGELOG.md.")
			var v overlap
			h.worker("A", v.runs("A", func(w *fakeWorker) AgentState {
				w.claim()
				edits.edit(w.wt, "CHANGELOG.md")
				w.beads.add("B", "second", 2)
				w.beads.describe("B", "Change internal/b.go"+also+", with a line in CHANGELOG.md.")
				time.Sleep(3 * readyPoll) // B takes the free slot at the next poll, if it may; then a few more
				return finishes("a.txt")(w)
			}))
			h.worker("B", v.runs("B", func(w *fakeWorker) AgentState {
				edits.edit(w.wt, "CHANGELOG.md")
				time.Sleep(2 * readyPoll) // a poll reads both workers' edits
				return finishes("b.txt")(w)
			}))
			o, code := h.run()
			if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
				t.Fatalf("%s: exit %d, final %q\n%s", c.name, code, o.Final(), h.sink.text())
			}
			if got := slices.Contains(v.of("B"), "A"); got != c.beside {
				t.Errorf("%s: B ran beside A: %v, want %v", c.name, got, c.beside)
			}
			log := h.logged()
			if !strings.Contains(log, c.footprint) {
				t.Errorf("%s: log lacks %q:\n%s", c.name, c.footprint, log)
			}
			if strings.Contains(log, "LIKELY_CONFLICT") {
				t.Errorf("%s: warned of a likely conflict:\n%s", c.name, log)
			}
			skip := "skipping B: touches "
			switch {
			case c.beside && strings.Contains(log, skip):
				t.Errorf("%s: B was skipped:\n%s", c.name, log)
			case c.shared && !strings.Contains(log, skip+"internal/y.go, like running A"):
				t.Errorf("%s: B wasn't skipped for internal/y.go:\n%s", c.name, log)
			case !c.union && !strings.Contains(log, skip+"CHANGELOG.md, like running A"):
				t.Errorf("%s: B wasn't skipped for CHANGELOG.md:\n%s", c.name, log)
			}
		})
	}
}

// A ticket naming only files git merges by union names nothing that keeps it apart from another,
// whether its text names them, its files metadata lists them or they are predicted for it.
func TestTicketFootprintLeavesOutFilesMergedByUnion(t *testing.T) {
	t.Parallel()
	repo := newRepoFiles([]string{"CHANGELOG.md", "docs/CHANGELOG.md", "internal/a.go"}, "")
	repo.union = func(f string) bool { return strings.HasSuffix(f, "CHANGELOG.md") }
	for _, tk := range []Ticket{
		{Description: "Add a line to CHANGELOG.md."},
		{Title: "Say hello", Metadata: []byte(`{"files": ["CHANGELOG.md", "docs/CHANGELOG.md"]}`)},
		{Title: "Say hello", Metadata: []byte(`{"predicted_files": ["CHANGELOG.md"]}`)},
	} {
		if fp := ticketFootprint(tk, repo); !fp.Empty() {
			t.Errorf("%+v: footprint %q, want empty", tk, fp)
		}
	}
	tk := Ticket{Description: "Change internal/a.go, with a line in CHANGELOG.md.",
		Metadata: []byte(`{"files": "docs/"}`)}
	want := []string{"docs/", "internal/a.go"} // a folder holding one stays: another ticket may add a file in it
	if fp := ticketFootprint(tk, repo); !slices.Equal(fp.Files, want) {
		t.Errorf("files %q, want %q", fp.Files, want)
	}
}
