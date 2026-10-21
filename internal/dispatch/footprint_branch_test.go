package dispatch

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
)

// A ticket with commits on its branch, as one reopened or carried over has, works where its branch
// does: its footprint is the files its branch changes and those its files metadata lists, with its
// area labels, not what its text names. The files git merges by union are left out of it too.
func TestBranchFootprint(t *testing.T) {
	t.Parallel()
	repo := newRepoFiles(trackedHere, "")
	repo.union = func(f string) bool { return f == "CHANGELOG.md" }
	reopened := Ticket{ID: "B", Description: "Fix internal/dispatch/loop.go in `Loop.merge`.",
		Labels: []string{"area:merge", "solo"}, Metadata: []byte(`{"files": "internal/tui/run.go"}`)}
	fp := branchFootprint(reopened, []string{"internal/dispatch/pick.go", "CHANGELOG.md"}, repo)
	want := Footprint{Files: []string{"internal/dispatch/pick.go", "internal/tui/run.go"},
		Areas: []string{"area:merge"}, Listed: true, Branch: true}
	if !slices.Equal(fp.Files, want.Files) || !slices.Equal(fp.Areas, want.Areas) || len(fp.Funcs) > 0 ||
		len(fp.Cited) > 0 || !fp.Listed || !fp.Branch {
		t.Errorf("footprint %+v, want %+v", fp, want)
	}
	if got := fp.String(); got != "internal/dispatch/pick.go, internal/tui/run.go, area:merge (from its branch)" {
		t.Errorf("logged as %q", got)
	}
	if what := shared(fp, TicketFootprint(Ticket{Description: "Change internal/dispatch/loop.go."}, trackedHere, ""),
		nil, nil); what != "" {
		t.Errorf("B overlaps a ticket over %q, which only its text names", what)
	}
	if what := shared(fp, TicketFootprint(Ticket{Description: "Change internal/dispatch/pick.go."}, trackedHere, ""),
		nil, nil); what != "internal/dispatch/pick.go" {
		t.Errorf("B overlaps a ticket changing its branch's file over %q", what)
	}
}

// In a run, a ready ticket whose branch has commits on top of the base is footprinted from them: one
// whose text names a running ticket's file, but whose branch changes another, runs beside it, and one
// whose branch changes that file waits for it, whatever its text names. A ticket whose branch has
// nothing on top of the base goes by its text.
func TestReopenedTicketsFootprintIsItsBranchs(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 3
		h.commitFiles("internal/dispatch/loop.go", "internal/dispatch/pick.go", "internal/tui/run.go")
		h.beads.add("A", "rework the loop", 1)
		h.beads.describe("A", "Split internal/dispatch/loop.go in two.")
		h.beads.add("B", "pick faster", 2)
		h.beads.describe("B", "Speed up picking. Reopened: internal/dispatch/loop.go calls it twice.")
		cutBranch(h.mem, "wt/B")
		h.mem.commit("wt/B", "B: pick faster", "internal/dispatch/pick.go")
		h.beads.add("C", "redraw the run", 3)
		h.beads.describe("C", "Redraw internal/tui/run.go.")
		cutBranch(h.mem, "wt/C")
		h.mem.commit("wt/C", "C: split the loop's start", "internal/dispatch/loop.go")
		h.beads.add("D", "loop docs", 4)
		h.beads.describe("D", "Comment internal/dispatch/loop.go.")
		cutBranch(h.mem, "wt/D") // nothing on top of main
		var v overlap
		// A and B each wait for the other: B's worker may be prompted first, and one that finished at
		// once would be gone before A's started, so the two would never be seen running together.
		for _, p := range [][2]string{{"A", "B"}, {"B", "A"}} {
			h.worker(p[0], v.runs(p[0], func(w *fakeWorker) AgentState {
				v.awaits(p[0], p[1], 20) // the other starts beside it, however long its start takes
				return finishes(strings.ToLower(p[0]) + ".txt")(w)
			}))
		}
		for _, id := range []string{"C", "D"} {
			h.worker(id, v.runs(id, finishes(strings.ToLower(id)+".txt")))
		}
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 4 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := v.of("B"); !slices.Contains(got, "A") {
			t.Errorf("B didn't run beside A, though its branch changes another file: ran beside %v", got)
		}
		for _, id := range []string{"C", "D"} {
			if got := v.of(id); slices.Contains(got, "A") {
				t.Errorf("%s ran beside A", id)
			}
		}
		log := h.logged()
		for _, want := range []string{
			"B footprint: internal/dispatch/pick.go (from its branch)\n",
			"skipping C: touches internal/dispatch/loop.go, like running A",
			"skipping D: touches internal/dispatch/loop.go, like running A",
		} {
			if !strings.Contains(log, want) {
				t.Errorf("the log lacks %q:\n%s", want, log)
			}
		}
		if strings.Contains(log, "skipping B") {
			t.Errorf("B was skipped:\n%s", log)
		}
	})
}
