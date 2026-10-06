package dispatch

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// resumeEditsReporter is editsReporter with the sessions its workers' hooks recorded, by worktree.
// As the hooks do, a new worker starts with no edits recorded, and a resumed one with those of the
// worker before it.
type resumeEditsReporter struct {
	editsReporter
	sessions map[string]Session
}

func (r *resumeEditsReporter) ReportArgs(wt string) ([]string, error) {
	r.mu.Lock()
	delete(r.edits, wt)
	r.mu.Unlock()
	return r.fakeReporter.ReportArgs(wt)
}

func (r *resumeEditsReporter) ResumeArgs(wt string) ([]string, error) {
	return r.fakeReporter.ReportArgs(wt)
}

func (r *resumeEditsReporter) Session(wt string) (Session, bool) {
	s, ok := r.sessions[wt]
	return s, ok
}

// The files a worker edited stay in its ticket's footprint once its session is resumed: a ticket
// naming one of them waits for the resumed worker, rather than running beside it.
func TestResumedWorkerKeepsItsEditsInItsFootprint(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.commitFiles("internal/x.go")
		r := &resumeEditsReporter{sessions: map[string]Session{
			h.worktree("A"): {ID: testSession, Transcript: "/home/u/.claude/projects/x/" + testSession + ".jsonl"},
		}}
		h.reporter = r
		h.cfg.Concurrency = 2
		h.beads.add("A", "first", 1) // names nothing
		var v overlap
		h.worker("A", v.runs("A", func(w *fakeWorker) AgentState {
			w.claim()
			r.edit(w.wt, "internal/x.go")
			w.beads.add("B", "second", 2)
			w.beads.describe("B", "Change internal/x.go.")
			return vanishes(w)
		}), v.runs("A", func(w *fakeWorker) AgentState {
			time.Sleep(3 * readyPoll) // a few polls with a slot free
			return finishes("a.txt")(w)
		}))
		h.worker("B", v.runs("B", finishes("b.txt")))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if starts := h.herdr.argsFor("A"); len(starts) != 2 || !resumedWith(starts[1], testSession) {
			t.Fatalf("starts %q; want A's worker resumed", starts)
		}
		if got := v.of("B"); slices.Contains(got, "A") {
			t.Errorf("B ran beside A's resumed worker, which edited the file it names")
		}
		if got := h.sink.dispatched(); !equal(got, []string{"A", "B"}) {
			t.Errorf("dispatched %v", got)
		}
		if log := h.logged(); !strings.Contains(log, "skipping B: touches internal/x.go, like running A") {
			t.Errorf("the skip was not logged:\n%s", log)
		}
	})
}
