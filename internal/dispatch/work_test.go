package dispatch

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestOutcomes(t *testing.T) {
	for status, want := range map[string]outcome{
		"closed": outcomeClosed, "deferred": outcomeDeferred, "in_progress": outcomePaused,
		"unknown": outcomeUnreadable, "open": outcomeUnfinished, "blocked": outcomeUnfinished,
	} {
		if got := outcomeOf(status); got != want {
			t.Errorf("outcomeOf(%q) = %v, want %v", status, got, want)
		}
	}
	if closedOutcomeOf("", false) != closedNoCommit || closedOutcomeOf("", true) != closedNoCommit {
		t.Error("a closed ticket without a commit must not merge")
	}
	if closedOutcomeOf("abc123 fix", true) != closedDirty {
		t.Error("a dirty worktree must not merge")
	}
	if closedOutcomeOf("abc123 fix", false) != closedMerge {
		t.Error("a commit and a clean worktree should merge")
	}
}

func TestStatusUnreadableSaysWhy(t *testing.T) {
	o, _, logged, code := brokenBdRun(t, promptAgents{})
	if code != ExitTool {
		t.Errorf("exit code %d, want %d", code, ExitTool)
	}
	want := "STATUS_UNREADABLE for A: bd defer A: exit status 1: Error: database is locked (another bd holds it); stopping"
	if !strings.HasPrefix(o.Final(), want) {
		t.Errorf("final line %q, want it to start with %q", o.Final(), want)
	}
	if !strings.Contains(logged, want) {
		t.Errorf("log lacks %q:\n%s", want, logged)
	}
}

func TestRunGoesOnPastATicketItsWorkerDefers(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A", func(w *fakeWorker) AgentState { w.claim(); w.deferIt(); return "idle" })
	h.worker("B", finishes("b.txt"))
	o := h.loop()
	o.StartTriage()
	code := o.Run(context.Background())
	o.FinishTriage(context.Background())
	if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := h.sink.of(EvDeferred); len(got) != 1 || !strings.HasPrefix(got[0], "A   A deferred by worker") {
		t.Errorf("deferred:\n%s", strings.Join(got, "\n"))
	}
	// Triage got the deferral, and fails here without claude.
	if !strings.Contains(h.sink.text(), "TRIAGE_FAILED for A") {
		t.Errorf("A was not triaged:\n%s", h.sink.text())
	}
	if !strings.Contains(h.mainLog(), "B: add b.txt") {
		t.Error("B should be merged")
	}
	if !exists(h.worktree("A")) || !equal(h.herdr.tabsClosed(), []string{"tab2"}) {
		t.Error("the deferred ticket's worktree and tab should be left open")
	}
	if got := o.setAside(); !equal(got, []string{"A"}) {
		t.Errorf("set aside: %v", got)
	}
}

func TestAFailedDeferKeepsTheTicketOutOfTheRun(t *testing.T) {
	// The worker never takes its prompt, and bd can't defer the ticket, so bd still lists it as
	// ready: it must not be dispatched again in this run.
	o, sink, logged, code := brokenBdRun(t, promptAgents{promptErr: fmt.Errorf("paste lost")})
	if code != ExitOK {
		t.Errorf("exit code %d, want %d", code, ExitOK)
	}
	var dispatched, deferred int
	var warned bool
	for _, ev := range sink.events {
		switch ev.Kind {
		case EvDispatch:
			dispatched++
		case EvDeferred:
			deferred++
		case EvWarn:
			warned = ev.Ticket == "A" && strings.Contains(ev.Text, "DEFER_FAILED") &&
				strings.Contains(ev.Text, "database is locked (another bd holds it)")
		}
	}
	if dispatched != 1 {
		t.Errorf("A dispatched %d times, want once:\n%s", dispatched, sink.text())
	}
	if deferred != 0 || !warned {
		t.Errorf("want a DEFER_FAILED warning with bd's error and no 'deferred' line:\n%s", sink.text())
	}
	if !strings.HasPrefix(o.Final(), "READY_EMPTY") {
		t.Errorf("final line %q", o.Final())
	}
	if !strings.Contains(logged, "database is locked") {
		t.Errorf("log lacks bd's error:\n%s", logged)
	}
}

// A worker asks the maintainer a question: its ticket leaves the queue and the run goes on. Once
// the question is answered the ticket comes back, to its old worktree, and the earlier worker still
// in its tab gives up the ticket's name to the new one.
func TestAskedTicketReturnsOnceAnswered(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A",
		func(w *fakeWorker) AgentState { w.claim(); w.ask("Q", "which way?"); return "idle" },
		finishes("a.txt"))
	h.worker("B", func(w *fakeWorker) AgentState {
		w.beads.set("Q", "closed") // the maintainer answers meanwhile
		return finishes("b.txt")(w)
	})
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := h.sink.of(EvAsked); len(got) != 1 || !strings.HasPrefix(got[0], "A   ASKED: A waits on your answer to Q (which way?)") {
		t.Errorf("asked:\n%s", strings.Join(got, "\n"))
	}
	if got := h.sink.of(EvDispatch); len(got) != 3 || !strings.HasPrefix(got[0], "A ") || !strings.HasPrefix(got[1], "B ") || !strings.HasPrefix(got[2], "A ") {
		t.Errorf("dispatched:\n%s", strings.Join(got, "\n"))
	}
	ev := h.sink.text()
	for _, want := range []string{"reusing worktree ", "/A (wt/A)", "earlier worker for A renamed to A-1"} {
		if !strings.Contains(ev, want) {
			t.Errorf("events lack %q:\n%s", want, ev)
		}
	}
	if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || !strings.Contains(log, "B: add b.txt") {
		t.Errorf("main:\n%s", log)
	}
	if got := h.herdr.tabsClosed(); !equal(got, []string{"tab2", "tab3"}) {
		t.Errorf("tabs closed: %v; the asking worker's tab1 should stay\n%s\nlog:\n%s", got, h.sink.text(), h.logged())
	}
}
