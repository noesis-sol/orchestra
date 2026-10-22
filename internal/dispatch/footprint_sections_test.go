package dispatch

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// runningDoc is a Markdown document of sections, one under another, with a heading in a code block
// that is none.
const runningDoc = "# Running orchestra\n\nIntro.\n\n" +
	"## Solo tickets\n\nSolo text.\n\n### Solo timing\n\nTiming text.\n\n" +
	"## Footprints\n\nFootprint text.\n\n```sh\n## not a heading\n```\n\n" +
	"## Merging ##\n\nMerge text.\n"

// sectionRepo is the repository's files with docs/running.md, whose text is runningDoc.
func sectionRepo() *repoFiles {
	repo := newRepoFiles(append(slices.Clone(trackedHere), "docs/running.md", "docs/other.md"), "")
	repo.read = func(file string) ([]byte, error) {
		if file == "docs/running.md" {
			return []byte(runningDoc), nil
		}
		return nil, os.ErrNotExist
	}
	return repo
}

// Two tickets on one Markdown document overlap only when the sections they name meet: the same
// section, however it is named (its heading, quoted or not, or its anchor, in any case), or one under
// the other; or when one names the document without a section.
func TestDocSectionsKeepTicketsApart(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		a, b   string
		shared bool
	}{
		{`Update docs/running.md § "Solo tickets".`, `Update docs/running.md#solo-tickets.`, true},
		{`Update docs/running.md#Solo-Tickets.`, `Update docs/running.md § “solo tickets”.`, true},
		{`Update docs/running.md § Solo tickets, then say so.`, `Update docs/running.md § 'Solo tickets'.`, true},
		{"Update `docs/running.md` § `Footprints`.", `Update docs/running.md (§ "Footprints").`, true},
		{`Update docs/running.md § "Solo tickets".`, `Update docs/running.md § "Footprints".`, false},
		{`Update docs/running.md#merging.`, `Update docs/running.md § "Footprints".`, false},
		{`Update docs/running.md § Solo tickets and the rest.`, `Update docs/running.md § "Footprints".`, false},
		// one under the other
		{`Update docs/running.md § "Solo timing".`, `Update docs/running.md § "Solo tickets".`, true},
		{`Update docs/running.md § "Solo timing".`, `Update docs/running.md § "Footprints".`, false},
		{`Update docs/running.md § "Running orchestra".`, `Update docs/running.md § "Merging".`, true},
		// the document whole
		{`Update docs/running.md.`, `Update docs/running.md § "Footprints".`, true},
		{`Update docs/running.md § "Solo tickets", as docs/running.md says.`, `Update docs/running.md#footprints.`, true},
		{`Update docs/running.md#L12.`, `Update docs/running.md#footprints.`, true},
		{`Update docs/running.md § Nowhere here.`, `Update docs/running.md#footprints.`, true},
		// a section a ticket adds
		{`Add docs/running.md § "Merge order".`, `Update docs/running.md § "Footprints".`, false},
		{`Add docs/running.md § "Merge order".`, `Update docs/running.md#merge-order.`, true},
		// a heading in a code block is none
		{`Update docs/running.md § "not a heading".`, `Update docs/running.md § "Footprints".`, false},
	} {
		repo := sectionRepo()
		a := ticketFootprint(Ticket{Description: c.a}, repo)
		b := ticketFootprint(Ticket{Description: c.b}, repo)
		want := ""
		if c.shared {
			want = "docs/running.md"
		}
		if what := shared(a, b, nil, nil); what != want {
			t.Errorf("%q beside %q: shared %q, want %q (%v; %v)", c.a, c.b, what, want, a.Sections, b.Sections)
		}
		if what := shared(b, a, nil, nil); what != want {
			t.Errorf("%q beside %q: shared %q, want %q (%v; %v)", c.b, c.a, what, want, b.Sections, a.Sections)
		}
	}
}

// The files metadata names a document's section as the text does, and its sections are the ones the
// ticket works on: those its text names it only reads.
func TestDocSectionsFromFilesMetadata(t *testing.T) {
	t.Parallel()
	repo := sectionRepo()
	listed := Ticket{Description: `Say why, as docs/running.md says.`,
		Metadata: []byte(`{"files": ["docs/running.md#footprints", "docs/running.md § Merging", "internal/dispatch/loop.go"]}`)}
	fp := ticketFootprint(listed, repo)
	want := []string{"running-orchestra/footprints", "running-orchestra/merging"}
	if got := fp.Sections["docs/running.md"]; !slices.Equal(got, want) {
		t.Errorf("sections %q, want %q (%+v)", got, want, fp)
	}
	if got, want := fp.String(), "docs/running.md#footprints, docs/running.md#merging, internal/dispatch/loop.go"; got != want {
		t.Errorf("footprint %q, want %q", got, want)
	}
	for text, shares := range map[string]bool{
		`Update docs/running.md § "Solo tickets".`: false,
		`Update docs/running.md § "Merging".`:      true,
		`Update docs/running.md.`:                  true,
	} {
		other := ticketFootprint(Ticket{Description: text}, repo)
		if what := shared(other, fp, nil, nil); (what != "") != shares {
			t.Errorf("%q beside the listed sections: shared %q, want shared %v", text, what, shares)
		}
	}
	whole := ticketFootprint(Ticket{Description: `Change docs/running.md § "Merging".`,
		Metadata: []byte(`{"files": ["docs/running.md"]}`)}, repo)
	if whole.Sections != nil {
		t.Errorf("listed whole, its text naming a section: sections %v, want none", whole.Sections)
	}
	branch := branchFootprint(listed, []string{"docs/other.md"}, repo)
	if want := []string{"docs/other.md", "docs/running.md", "internal/dispatch/loop.go"}; !slices.Equal(branch.Files, want) || branch.Sections != nil {
		t.Errorf("branch footprint %+v, want files %q whole", branch, want)
	}
}

// A running worker's edits to a document hold back a ticket on the sections it edited only: text under
// a section's heading but before its subsections is none of theirs. Edits whose sections aren't known
// count for the whole document.
func TestDocSectionEditsHoldBackOnlyTheirSections(t *testing.T) {
	t.Parallel()
	edit := func(section, line string) string {
		return strings.Replace(runningDoc, section, section+line, 1)
	}
	footprints := changedSections(runningDoc, edit("Footprint text.\n", "More.\n"))
	soloOwn := changedSections(runningDoc, edit("Solo text.\n", "More.\n"))
	timing := changedSections(runningDoc, edit("Timing text.\n", "More.\n"))
	if want := []string{"running-orchestra/footprints"}; !slices.Equal(footprints, want) {
		t.Fatalf("changed sections %q, want %q", footprints, want)
	}
	repo := sectionRepo()
	running := ticketFootprint(Ticket{Description: "Change internal/dispatch/loop.go."}, repo)
	edited := []string{"docs/running.md"}
	for _, c := range []struct {
		ready string
		edits []string // nil: not known
		want  string
	}{
		{`Update docs/running.md § "Footprints".`, footprints, "docs/running.md"},
		{`Update docs/running.md § "Solo tickets".`, footprints, ""},
		{`Update docs/running.md.`, footprints, "docs/running.md"},
		{`Update docs/running.md § "Running orchestra".`, footprints, "docs/running.md"},
		{`Update docs/running.md § "Solo tickets".`, nil, "docs/running.md"},
		{`Update docs/running.md § "Solo timing".`, soloOwn, ""},
		{`Update docs/running.md § "Solo tickets".`, timing, "docs/running.md"},
		{`Update docs/other.md § "Footprints".`, footprints, ""},
	} {
		r := running
		if c.edits != nil {
			r.edits = map[string][]string{"docs/running.md": c.edits}
		}
		ready := ticketFootprint(Ticket{Description: c.ready}, repo)
		if what := shared(ready, r, edited, nil); what != c.want {
			t.Errorf("%q beside edits to %q: shared %q, want %q", c.ready, c.edits, what, c.want)
		}
	}
	// The running ticket's own sections count beside its edits.
	named := ticketFootprint(Ticket{Description: `Update docs/running.md § "Merging".`}, repo)
	named.edits = map[string][]string{"docs/running.md": footprints}
	for text, want := range map[string]string{
		`Update docs/running.md § "Merging".`:      "docs/running.md",
		`Update docs/running.md § "Footprints".`:   "docs/running.md",
		`Update docs/running.md § "Solo tickets".`: "",
	} {
		if what := shared(ticketFootprint(Ticket{Description: text}, repo), named, edited, nil); what != want {
			t.Errorf("%q beside a ticket on Merging that edited Footprints: shared %q, want %q", text, what, want)
		}
	}
}

// changedSections finds the sections whose text an edit changed, added or removed; a heading in a code
// block is no section, and the text before the first heading is the section "".
func TestChangedSections(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name, after string
		want        []string
	}{
		{"unchanged", runningDoc, nil},
		{"in a code block", strings.Replace(runningDoc, "## not a heading", "## still not", 1),
			[]string{"running-orchestra/footprints"}},
		{"a section added", strings.Replace(runningDoc, "## Merging", "## Merge order\n\nOrder.\n\n## Merging", 1),
			[]string{"running-orchestra/merge-order"}},
		{"a section removed", strings.Replace(runningDoc, "### Solo timing\n\nTiming text.\n\n", "", 1),
			[]string{"running-orchestra/solo-tickets/solo-timing"}},
		{"a heading renamed", strings.Replace(runningDoc, "## Merging ##", "## Merges", 1),
			[]string{"running-orchestra/merges", "running-orchestra/merging"}},
		{"above every heading", "Front.\n" + runningDoc, []string{""}},
	} {
		if got := changedSections(runningDoc, c.after); !slices.Equal(got, c.want) {
			t.Errorf("%s: changed %q, want %q", c.name, got, c.want)
		}
	}
	if got, want := changedSections("", "# A\n\n## B\n"), []string{"a", "a/b"}; !slices.Equal(got, want) {
		t.Errorf("a new document: changed %q, want %q", got, want)
	}
}

// Code files have no sections: what follows a code file's name changes nothing, and two tickets on
// one code file overlap as they did.
func TestCodeFilesHaveNoSections(t *testing.T) {
	t.Parallel()
	repo := sectionRepo()
	a := ticketFootprint(Ticket{Description: `Change internal/dispatch/loop.go § "Merging".`}, repo)
	b := ticketFootprint(Ticket{Description: `Change internal/dispatch/loop.go#footprints.`}, repo)
	if a.Sections != nil || b.Sections != nil {
		t.Errorf("sections of a code file: %v, %v", a.Sections, b.Sections)
	}
	if what := shared(a, b, nil, nil); what != "internal/dispatch/loop.go" {
		t.Errorf("shared %q, want internal/dispatch/loop.go", what)
	}
	edits := Footprint{edits: map[string][]string{"internal/dispatch/loop.go": {"merging"}}}
	if what := shared(a, edits, []string{"internal/dispatch/loop.go"}, nil); what != "internal/dispatch/loop.go" {
		t.Errorf("beside an edit: shared %q, want internal/dispatch/loop.go", what)
	}
}

// commitContent adds a commit to branch that writes content to file.
func (g *fakeGit) commitContent(branch, file, content string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	c := g.newCommit("files", file)
	c.content = map[string]string{file: content}
	g.branches[branch] = append(g.branches[branch], c)
}

// worktreeEdits is editsReporter reporting no edits in a worktree removed, as Claude Code's hooks'
// records go with it.
type worktreeEdits struct{ editsReporter }

func (r *worktreeEdits) EditedFiles(wt string) []string {
	if _, err := os.Stat(wt); err != nil {
		return nil
	}
	return r.editsReporter.EditedFiles(wt)
}

// In a run, a ticket on a section of a document runs beside a worker that edits another section of it,
// and waits for one that edits its own; two workers editing different sections are no likely conflict.
func TestRunKeepsTicketsApartByDocSection(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.mem.commitContent("main", "docs/running.md", runningDoc)
		h.commitFiles("internal/a.go")
		if err := os.MkdirAll(filepath.Join(h.repo, "docs"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.repo, "docs/running.md"), []byte(runningDoc), 0o644); err != nil {
			t.Fatal(err)
		}
		edits := &worktreeEdits{}
		h.reporter = edits
		h.cfg.Concurrency = 3
		editing := func(w *fakeWorker, from, to string) {
			if err := os.MkdirAll(filepath.Join(w.wt, "docs"), 0o755); err != nil {
				t.Error(err)
			}
			w.write(w.wt, "docs/running.md", strings.Replace(runningDoc, from, to, 1))
			edits.edit(w.wt, "docs/running.md")
		}
		h.beads.add("A", "first", 1)
		h.beads.describe("A", "Change internal/a.go.")
		var v overlap
		h.worker("A", v.runs("A", func(w *fakeWorker) AgentState {
			w.claim()
			editing(w, "Footprint text.", "Footprint text, edited.")
			w.beads.add("B", "second", 2)
			w.beads.describe("B", `Update docs/running.md § "Solo tickets".`)
			w.beads.add("C", "third", 3)
			w.beads.describe("C", `Update docs/running.md#footprints.`)
			time.Sleep(4 * readyPoll) // B takes a free slot at the next poll, C may not; then a few more
			return finishes("a.txt")(w)
		}))
		h.worker("B", v.runs("B", func(w *fakeWorker) AgentState {
			editing(w, "Solo text.", "Solo text, edited.")
			time.Sleep(2 * readyPoll) // a poll reads both workers' edits
			return finishes("b.txt")(w)
		}))
		h.worker("C", v.runs("C", finishes("c.txt")))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if !slices.Contains(v.of("B"), "A") {
			t.Errorf("B didn't run beside A, which edits another section")
		}
		if slices.Contains(v.of("C"), "A") {
			t.Errorf("C ran beside A, which edits its section")
		}
		log := h.logged()
		for _, want := range []string{"B footprint: docs/running.md#solo-tickets\n",
			"skipping C: touches docs/running.md, like running A"} {
			if !strings.Contains(log, want) {
				t.Errorf("log lacks %q:\n%s", want, log)
			}
		}
		if h.sink.warned("LIKELY_CONFLICT", "") {
			t.Errorf("warned of a likely conflict:\n%s", log)
		}
	})
}
