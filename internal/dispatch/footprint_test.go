package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var trackedHere = []string{
	"README.md", "CHANGELOG.md", "internal/dispatch/loop.go", "internal/dispatch/run.go", "internal/dispatch/merge.go",
	"internal/claude/claude.go", "internal/tui/run.go", ".orchestra/settings.json",
}

func TestTicketFootprintNamesFilesAndFunctions(t *testing.T) {
	tk := Ticket{ID: "x", Title: "Hand a conflict back from Loop.merge",
		Description: "When `Loop.merge` (internal/dispatch/loop.go:1125) aborts, call `o.agentName(id)` and `deliverPrompt`, " +
			"then refreshBranch() and `os.WriteFile(p, b, 0o644)`. The hooks write .orchestra/run/activity.json (internal/claude/claude.go). " +
			"See https://github.com/gastownhall/beads/blob/main/README.md, e.g. `go test ./...` and v0.2.0.",
		Notes: "Moved to dispatch/merge.go; update settings.json and README.md. Add internal/dispatch/handback.go. " +
			"Not /Users/me/Projects/x/y.go, nor internal/nowhere/z.go, nor (`solo`, `RESOLVING`).",
		AcceptanceCriteria: "Tests in a new file(s)."}
	fp := TicketFootprint(tk, trackedHere)
	wantFiles := []string{".orchestra/settings.json", "README.md", "internal/claude/claude.go", "internal/dispatch/handback.go",
		"internal/dispatch/loop.go", "internal/dispatch/merge.go"}
	if !slices.Equal(fp.Files, wantFiles) {
		t.Errorf("files %q, want %q", fp.Files, wantFiles)
	}
	if want := []string{"Loop.merge", "agentName", "deliverPrompt", "refreshBranch"}; !slices.Equal(fp.Funcs, want) {
		t.Errorf("functions %q, want %q", fp.Funcs, want)
	}
	if got := fp.String(); got != "Loop.merge, agentName, deliverPrompt, refreshBranch, .orchestra/settings.json, README.md, internal/claude/claude.go, internal/dispatch/handback.go, internal/dispatch/loop.go, internal/dispatch/merge.go" {
		t.Errorf("String() = %q", got)
	}

	// A bare name means every file it can: run.go is in two folders.
	if fp := TicketFootprint(Ticket{Description: "Picking lives in run.go."}, trackedHere); !slices.Equal(fp.Files, []string{"internal/dispatch/run.go", "internal/tui/run.go"}) {
		t.Errorf("bare name: %q", fp.Files)
	}
	// Without the repository's files, a name with a folder or a source extension is kept.
	if fp := TicketFootprint(tk, nil); !slices.Contains(fp.Files, "internal/nowhere/z.go") || !slices.Contains(fp.Files, "settings.json") ||
		!slices.Contains(fp.Files, "dispatch/merge.go") || slices.Contains(fp.Files, "Loop.merge") {
		t.Errorf("unknown repository: %q", fp.Files)
	}
	if fp := TicketFootprint(Ticket{Title: "Say hello", Description: "Nothing in particular, e.g. a nicer greeting."}, trackedHere); !fp.Empty() || fp.String() != "nothing named" {
		t.Errorf("a ticket naming nothing: %+v", fp)
	}
}

func TestTicketFootprintFromLabelsAndMetadata(t *testing.T) {
	for _, meta := range []string{
		`{"files": ["internal/tui/run.go", "docs/new.md"], "team": "x"}`,
		`{"files": "internal/tui/run.go, docs/new.md"}`,
		`"{\"files\":\"internal/tui/run.go docs/new.md\"}"`, // bd may give the object as a string
	} {
		tk := Ticket{Labels: []string{"scheduling", "area:tui", "area:"}, Metadata: []byte(meta)}
		fp := TicketFootprint(tk, trackedHere)
		if !slices.Equal(fp.Files, []string{"docs/new.md", "internal/tui/run.go"}) || !slices.Equal(fp.Areas, []string{"area:tui"}) {
			t.Errorf("%s: %+v", meta, fp)
		}
	}
	if fp := TicketFootprint(Ticket{Metadata: []byte(`{"files": 3}`)}, trackedHere); !fp.Empty() {
		t.Errorf("unreadable files entry: %+v", fp)
	}
}

// The predicted files count only for a ticket naming nothing else.
func TestTicketFootprintFallsBackOnPredictedFiles(t *testing.T) {
	predicted := []byte(`{"predicted_files": "internal/dispatch/run.go,internal/nowhere/z.go", "files": []}`)
	fp := TicketFootprint(Ticket{Title: "Say hello", Metadata: predicted}, trackedHere)
	if !slices.Equal(fp.Files, []string{"internal/dispatch/run.go"}) || !fp.Predicted {
		t.Errorf("predicted: %+v", fp)
	}
	if got := fp.String(); got != "internal/dispatch/run.go (predicted)" {
		t.Errorf("String() = %q", got)
	}
	fp = TicketFootprint(Ticket{Description: "Change README.md.", Metadata: predicted}, trackedHere)
	if !slices.Equal(fp.Files, []string{"README.md"}) || fp.Predicted {
		t.Errorf("a ticket naming a file: %+v", fp)
	}
	if fp := TicketFootprint(Ticket{Labels: []string{"area:tui"}, Metadata: predicted}, trackedHere); len(fp.Files) > 0 || fp.Predicted {
		t.Errorf("a ticket with an area: %+v", fp)
	}
}

func TestSharedComparesFunctionsWhenBothNameThem(t *testing.T) {
	merge := Footprint{Files: []string{"merge.go"}, Funcs: []string{"Loop.merge"}}
	cases := []struct {
		name         string
		ready, run   Footprint
		edited, want string
	}{
		{"same function", Footprint{Funcs: []string{"merge"}}, merge, "", "merge"},
		{"another type's method", Footprint{Funcs: []string{"Git.merge"}}, merge, "", ""},
		{"other functions in the same file", Footprint{Files: []string{"merge.go"}, Funcs: []string{"runCheck"}}, merge, "", ""},
		{"a file, one naming no functions", Footprint{Files: []string{"merge.go"}}, merge, "", "merge.go"},
		{"different files", Footprint{Files: []string{"run.go"}}, merge, "", ""},
		{"a shared area", Footprint{Areas: []string{"area:tui"}}, Footprint{Areas: []string{"area:tui"}}, "", "area:tui"},
		{"a file the worker edited beyond its ticket", Footprint{Files: []string{"run.go"}, Funcs: []string{"pickNext"}}, merge, "run.go", "run.go"},
		{"a named file the worker edits", Footprint{Files: []string{"merge.go"}, Funcs: []string{"runCheck"}}, merge, "merge.go", ""},
		{"nothing named", Footprint{}, merge, "merge.go", ""},
	}
	for _, c := range cases {
		var edited []string
		if c.edited != "" {
			edited = []string{c.edited}
		}
		if got := shared(c.ready, c.run, edited); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// editsReporter reports the files each worktree's worker edited, as the hooks would.
type editsReporter struct {
	fakeReporter
	mu    sync.Mutex
	edits map[string][]string
}

func (r *editsReporter) edit(wt string, files ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.edits == nil {
		r.edits = map[string][]string{}
	}
	r.edits[wt] = append(r.edits[wt], files...)
}

func (r *editsReporter) EditedFiles(wt string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.edits[wt]...)
}

// describe gives the ticket a description.
func (b *fakeBeads) describe(id, text string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tickets[id].Description = text
}

// commitFiles adds files to the harness repository's main branch, so tickets can name them.
func (h *harness) commitFiles(files ...string) {
	for _, f := range files {
		if err := os.MkdirAll(filepath.Join(h.repo, filepath.Dir(f)), 0o755); err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(h.repo, f), []byte("package x\n"), 0o644); err != nil {
			h.t.Fatal(err)
		}
	}
	h.git(h.repo, "add", ".")
	h.git(h.repo, "commit", "-q", "-m", "files")
}

// dispatchedYet reports whether the ticket has been dispatched.
func (s *runSink) dispatchedYet(id string) bool { return slices.Contains(s.dispatched(), id) }

// Two ready tickets naming the same function never run together, while one that names another
// takes the free slot.
func TestOverlappingTicketsNeverRunTogether(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.Concurrency = 2
	h.beads.add("A", "fix the merge", 1)
	h.beads.describe("A", "When `Loop.merge` aborts a rebase, say why.")
	h.beads.add("B", "time the merge", 2)
	h.beads.describe("B", "Log how long Loop.merge() takes.")
	h.beads.add("C", "pick faster", 3)
	h.beads.describe("C", "Speed up pickNext().")
	var v overlap
	h.worker("A", v.runs("A", func(w *fakeWorker) string {
		eventually(t, "C never took the free slot", func() bool { return h.sink.dispatchedYet("C") })
		time.Sleep(20 * time.Millisecond) // a few more polls, with C finished and a slot free
		return finishes("a.txt")(w)
	}))
	h.worker("B", v.runs("B", finishes("b.txt")))
	h.worker("C", v.runs("C", finishes("c.txt")))
	o := h.loop()
	o.wait.ready = 5 * time.Millisecond
	if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := v.of("B"); slices.Contains(got, "A") {
		t.Errorf("B ran beside A")
	}
	if got := h.sink.dispatched(); !equal(got, []string{"A", "C", "B"}) {
		t.Errorf("dispatched %v", got)
	}
	log := h.logged()
	if n := strings.Count(log, "skipping B: touches Loop.merge, like running A"); n != 1 {
		t.Errorf("the skip was logged %d times, want once:\n%s", n, log)
	}
	if !strings.Contains(log, "A footprint: Loop.merge\n") || !strings.Contains(log, "C footprint: pickNext\n") {
		t.Errorf("footprints not logged at dispatch:\n%s", log)
	}
}

// Files a worker edits extend its ticket's footprint: a ticket naming one waits, and one naming
// another file takes the free slot.
func TestEditsExtendARunningTicketsFootprint(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.commitFiles("internal/x.go", "internal/y.go")
	edits := &editsReporter{}
	h.reporter = edits
	h.cfg.Concurrency = 2
	h.beads.add("A", "first", 1) // names nothing
	var v overlap
	h.worker("A", v.runs("A", func(w *fakeWorker) string {
		w.claim()
		edits.edit(w.wt, "internal/x.go")
		w.beads.add("B", "second", 2)
		w.beads.describe("B", "Change internal/x.go.")
		w.beads.add("C", "third", 3)
		w.beads.describe("C", "Change internal/y.go.")
		eventually(t, "C never took the free slot", func() bool { return h.sink.dispatchedYet("C") })
		time.Sleep(20 * time.Millisecond) // a few more polls
		return finishes("a.txt")(w)
	}))
	h.worker("B", v.runs("B", finishes("b.txt")))
	h.worker("C", v.runs("C", finishes("c.txt")))
	o := h.loop()
	o.wait.ready = 5 * time.Millisecond
	if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := v.of("B"); slices.Contains(got, "A") {
		t.Errorf("B ran beside A, which edits the file it names")
	}
	if got := h.sink.dispatched(); !equal(got, []string{"A", "C", "B"}) {
		t.Errorf("dispatched %v", got)
	}
	if log := h.logged(); strings.Count(log, "skipping B: touches internal/x.go, like running A") != 1 {
		t.Errorf("the skip was not logged once:\n%s", log)
	}
}

// Tickets that name nothing run side by side in priority order, as they did before footprints; so
// do overlapping tickets when the project turns footprints off.
func TestWithoutFootprintsTicketsRunAsBefore(t *testing.T) {
	t.Parallel()
	for _, off := range []bool{false, true} {
		h := newHarness(t)
		h.cfg.Concurrency = 2
		h.cfg.NoFootprint = off
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.add("C", "third", 3)
		if off {
			h.beads.describe("A", "Fix Loop.merge().")
			h.beads.describe("B", "Fix Loop.merge().")
		}
		var v overlap
		h.worker("A", v.runs("A", func(w *fakeWorker) string {
			eventually(t, "B never ran beside A", func() bool { return h.sink.dispatchedYet("B") })
			return finishes("a.txt")(w)
		}))
		h.worker("B", v.runs("B", finishes("b.txt")))
		h.worker("C", v.runs("C", finishes("c.txt")))
		o := h.loop()
		o.wait.ready = 5 * time.Millisecond
		if code := o.Run(context.Background()); code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("off=%v: exit %d, final %q\n%s", off, code, o.Final(), h.sink.text())
		}
		if got := h.sink.dispatched(); !equal(got, []string{"A", "B", "C"}) {
			t.Errorf("off=%v: dispatched %v", off, got)
		}
		if log := h.logged(); strings.Contains(log, "skipping") {
			t.Errorf("off=%v: a ticket was skipped:\n%s", off, log)
		}
		if log := h.logged(); off && strings.Contains(log, "footprint") {
			t.Errorf("footprints logged with them off:\n%s", log)
		}
	}
}

// Two running workers editing the same file are warned about once: the second to merge is likely
// to conflict.
func TestTwoWorkersEditingOneFileAreWarnedAbout(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	edits := &editsReporter{}
	h.reporter = edits
	h.cfg.Concurrency = 2
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	const warning = "LIKELY_CONFLICT: A and B both edit internal/x.go; the second to merge may conflict"
	both := func(file string) behaviour {
		return func(w *fakeWorker) string {
			edits.edit(w.wt, "internal/x.go")
			eventually(t, "no warning", func() bool { return strings.Contains(h.sink.text(), warning) })
			time.Sleep(20 * time.Millisecond) // a few more polls
			return finishes(file)(w)
		}
	}
	h.worker("A", both("a.txt"))
	h.worker("B", both("b.txt"))
	o := h.loop()
	o.wait.ready = 5 * time.Millisecond
	if code := o.Run(context.Background()); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, h.sink.text())
	}
	if n := strings.Count(h.logged(), warning); n != 1 {
		t.Errorf("warned %d times, want once:\n%s", n, h.logged())
	}
	if !h.alerts.has("  " + warning) {
		t.Errorf("no notification: %q", h.alerts.list())
	}
}
