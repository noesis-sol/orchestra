package dispatch

import (
	"context"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// sessionReporter is hookReporter with the sessions its workers' hooks recorded, by worktree, and
// the ends of their transcripts. They are set before the run, and only read during it.
type sessionReporter struct {
	hookReporter
	sessions map[string]Session
	tails    map[string]string
}

func (r *sessionReporter) Session(wt string) (Session, bool) {
	s, ok := r.sessions[wt]
	return s, ok
}

func (r *sessionReporter) TranscriptTail(wt string) string { return r.tails[wt] }

const testSession = "0b7c4a52-3f0e-4c5e-9a43-5d1f2c3b4a59"

// withSession gives h a reporter that has ticket id's worker's session recorded.
func withSession(h *harness, id string) *sessionReporter {
	r := &sessionReporter{sessions: map[string]Session{
		h.worktree(id): {ID: testSession, Transcript: "/home/u/.claude/projects/x/" + testSession + ".jsonl"},
	}}
	h.reporter = r
	return r
}

// vanishes claims the ticket and works for a while, then is gone from its tab, its ticket in progress.
func vanishes(w *fakeWorker) AgentState {
	w.claim()
	time.Sleep(time.Minute)
	return StateGone // Claude Code quit, say
}

// resumedWith reports whether args, as fakeHerdr records a start, resume session.
func resumedWith(args []string, session string) bool {
	i := slices.Index(args, "--resume")
	return i >= 0 && i+1 < len(args) && args[i+1] == session
}

// A worker gone from its tab with its ticket in progress is started again in a new tab, resuming its
// session and told to carry on, rather than stopping the run; it finishes the ticket, which merges.
func TestGoneWorkerIsResumedWithItsSession(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.worker("A", vanishes, finishes("a.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v; want A", got)
		}
		starts := h.herdr.argsFor("A")
		if len(starts) != 2 || resumedWith(starts[0], testSession) || !resumedWith(starts[1], testSession) {
			t.Fatalf("starts %q; want a new worker, then one resuming %s", starts, testSession)
		}
		if msg := starts[1][len(starts[1])-1]; !strings.HasPrefix(msg, "Orchestra: your worker stopped") ||
			!strings.Contains(msg, "Carry on with A where you left off") {
			t.Errorf("the resumed worker was told %q", msg)
		}
		want := "A   RESUMED: A's worker is gone from tab tab1 with the ticket in progress; resuming its session " +
			testSession + " in a new tab (worktree " + h.worktree("A") + ")"
		if got := warnings(h, "A"); !equal(got, []string{want}) {
			t.Errorf("warnings %q\nwant %q", got, want)
		}
		if ev := h.sink.text(); !strings.Contains(ev, "A's worker carries on in tab tab2, its session resumed; adopting it") {
			t.Errorf("events:\n%s", ev)
		}
		if notes := h.beads.notesOf("A"); !strings.Contains(notes, "its session "+testSession+" is resumed in a new tab") {
			t.Errorf("notes on A: %q", notes)
		}
	})
}

// A worker resumed once and gone again stops the run (PAUSED), saying the next run resumes it; the
// next run, carrying it over, does, and its ticket merges.
func TestGoneWorkerIsResumedOncePerRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.worker("A", vanishes, vanishes, finishes("a.txt"))
		o, code := h.run()
		want := "PAUSED: A still in_progress, its worker gone from tab tab2 (worktree " + h.worktree("A") +
			"); its session " + testSession + " was resumed once in this run already, and the next run resumes it again; " +
			"stopping so it can be looked at"
		if code != ExitStuck || o.Final() != want {
			t.Fatalf("run 1: exit %d, final %q\nwant %q\n%s", code, o.Final(), want, h.sink.text())
		}
		if n := len(h.herdr.argsFor("A")); n != 2 {
			t.Fatalf("run 1 started %d workers on A; want 2", n)
		}

		o, code = h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" { // adopted, not dispatched
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if starts := h.herdr.argsFor("A"); len(starts) != 3 || !resumedWith(starts[2], testSession) {
			t.Fatalf("starts %q; want run 2 to resume %s", starts, testSession)
		}
		if ev := h.sink.text(); !strings.Contains(ev, "RESUMED: A's worker is gone from tab tab2 with the ticket in progress") {
			t.Errorf("run 2 should resume A's worker gone from tab2:\n%s", ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v; want A", got)
		}
		if a, _ := h.beads.Show(t.Context(), "A"); HasLabel(a, UnmergedLabel) {
			t.Errorf("A merged, so its label should be removed: %v", a.Labels)
		}
	})
}

// Without a session to resume, a worker gone from its tab with its ticket in progress stops the run
// (PAUSED), as before, and no other worker is started on the ticket.
func TestGoneWorkerWithoutASessionPausesTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.reporter = &hookReporter{}
		h.beads.add("A", "first", 1)
		h.worker("A", vanishes)
		o, code := h.run()
		want := "PAUSED: A still in_progress, its worker gone from tab tab1 (worktree " + h.worktree("A") +
			"); stopping so it can be looked at"
		if code != ExitStuck || o.Final() != want {
			t.Fatalf("exit %d, final %q\nwant %q\n%s", code, o.Final(), want, h.sink.text())
		}
		if n := len(h.herdr.argsFor("A")); n != 1 {
			t.Errorf("started %d workers on A; want 1", n)
		}
		if notes := h.beads.notesOf("A"); !strings.Contains(notes, "the worker in Herdr tab tab1 is gone") {
			t.Errorf("notes on A: %q", notes)
		}
	})
}

// An asked ticket whose worker, back at work after its question, is gone from its tab has its
// session resumed, told its question is answered, rather than stopping the run.
func TestAskedTicketWhoseWorkerIsGoneIsResumed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		r := withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		answered := make(chan struct{})
		h.worker("A", asksThenCarriesOn(&r.hookReporter, answered, func(w *fakeWorker) AgentState {
			time.Sleep(time.Minute)
			w.claim()
			return StateGone
		}), finishes("a.txt"))
		h.worker("B", answersAfter(time.Minute+7*time.Second, answered, time.Hour, "b.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		starts := h.herdr.argsFor("A")
		if len(starts) != 2 || !resumedWith(starts[1], testSession) {
			t.Fatalf("starts %q; want A's worker resumed", starts)
		}
		if msg := starts[1][len(starts[1])-1]; !strings.Contains(msg, "Your question Q is answered: read the answer with bd show Q.") {
			t.Errorf("the resumed worker was told %q", msg)
		}
		if got := closedIDs(h); !equal(got, []string{"A", "B"}) {
			t.Errorf("merged %v; want A, then B", got)
		}
	})
}

// Triage's evidence carries the end of the worker's transcript, as its reporter reads it.
func TestTriageEvidenceHasTheTranscript(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		r := withSession(h, "A")
		r.tails = map[string]string{h.worktree("A"): "tool Bash: go test ./...\ntool error: exit status 1"}
		h.beads.add("A", "first", 1)
		d := h.loop().gatherDeferral(context.Background(), "A", "the worker deferred it", h.worktree("A"))
		if d.Transcript != r.tails[h.worktree("A")] {
			t.Errorf("transcript %q", d.Transcript)
		}
		h.reporter = nil
		if d := h.loop().gatherDeferral(context.Background(), "A", "the worker deferred it", h.worktree("A")); d.Transcript != "" {
			t.Errorf("without a reporter, transcript %q", d.Transcript)
		}
	})
}
