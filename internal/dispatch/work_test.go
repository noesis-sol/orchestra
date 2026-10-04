package dispatch

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

func TestOutcomes(t *testing.T) {
	for status, want := range map[TicketStatus]outcome{
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
	// Triage got the deferral, and fails here without claude; A stays deferred, not set aside for review.
	if !strings.Contains(h.sink.text(), "TRIAGE_FAILED for A") {
		t.Errorf("A was not triaged:\n%s", h.sink.text())
	}
	for _, ev := range h.sink.events {
		if ev.Aside {
			t.Errorf("set aside: %q", ev.Text)
		}
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
			warned = ev.Ticket == "A" && ev.Aside && strings.Contains(ev.Text, "DEFER_FAILED") &&
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
// the question is answered the ticket comes back, and its earlier worker, idle in its tab, is told
// so and carries on there with what it knows; its work is merged in the same run. It left its
// ticket in progress, so it settles only after the idle grace.
func TestAskedTicketReturnsOnceAnswered(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A",
			func(w *fakeWorker) AgentState { w.claim(); w.ask("Q", "which way?"); return "idle" },
			finishes("a.txt")) // once told the answer is in
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
		if got := h.sink.of(EvAnswered); !equal(got, []string{"A   ANSWERED: Q (which way?) is answered, so A comes back"}) {
			t.Errorf("answered:\n%s", strings.Join(got, "\n"))
		}
		if got := h.sink.of(EvDispatch); len(got) != 3 || !strings.HasPrefix(got[0], "A ") || !strings.HasPrefix(got[1], "B ") ||
			!strings.HasPrefix(got[2], "A ") {
			t.Errorf("dispatched:\n%s", strings.Join(got, "\n"))
		}
		if ev := h.sink.text(); !strings.Contains(ev, "A's earlier worker in tab tab1 was told Q is answered and carries on; adopting it") ||
			strings.Contains(ev, "renamed") {
			t.Errorf("the earlier worker should have been told the answer is in, not replaced:\n%s", ev)
		}
		if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || !strings.Contains(log, "B: add b.txt") {
			t.Errorf("main:\n%s", log)
		}
		if got := h.herdr.tabsClosed(); !equal(got, []string{"tab2", "tab1"}) {
			t.Errorf("tabs closed: %v; A's work should be merged from tab1, with no third tab\n%s\nlog:\n%s", got, h.sink.text(), h.logged())
		}
		if got := h.herdr.pastedTo(); !equal(got, []string{"A"}) {
			t.Errorf("pasted to %v; want the answer's news to A's worker only", got)
		}
	})
}

// The maintainer answers in the asking worker's tab, and it carries on before the run sees the
// answer. When the ticket comes back the run adopts that worker, still working, rather than stop
// for it: it waits for it to settle and merges its work, without touching its worktree before.
func TestAskedTicketWhoseWorkerCarriesOnIsAdopted(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		answered := make(chan struct{})
		showsA := make(chan func(AgentState), 1) // what Herdr shows A's worker as
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.ask("Q", "which way?")
			showsA <- w.shows
			w.shows("idle") // asked, and stopped
			<-answered
			time.Sleep(time.Minute) // the run adopts it meanwhile
			w.claim()
			w.commit("a.txt")
			w.close()
			return "idle"
		})
		h.worker("B", func(w *fakeWorker) AgentState {
			// A is answered in its tab and works again before B is done: the run reads A's status
			// as soon as it has merged B, at the same instant A's own goroutine would wake.
			(<-showsA)("working")
			w.beads.set("Q", "closed")
			close(answered)
			return finishes("b.txt")(w)
		})
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if len(h.sink.of(EvHold)) != 0 || len(h.sink.of(EvAnswered)) != 1 {
			t.Errorf("want one ANSWERED and no HOLD:\n%s", h.sink.text())
		}
		ev := h.sink.text()
		if !strings.Contains(ev, "A's earlier worker is still working in tab tab1; adopting it rather than starting another") {
			t.Errorf("A's worker was not adopted:\n%s", ev)
		}
		if strings.Contains(ev, "reusing worktree") || strings.Contains(ev, "renamed") {
			t.Errorf("the adopted worker's worktree was prepared again, or the worker renamed:\n%s", ev)
		}
		if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") || !strings.Contains(log, "B: add b.txt") {
			t.Errorf("main:\n%s", log)
		}
		if got := h.herdr.tabsClosed(); !equal(got, []string{"tab2", "tab1"}) {
			t.Errorf("tabs closed: %v; A's work should be merged from tab1, with no third tab\n%s", got, h.sink.text())
		}
		if got := h.herdr.pastedTo(); len(got) != 0 {
			t.Errorf("pasted to %v; a working worker is left to work", got)
		}
	})
}

// An earlier worker still at work on a ticket that was not asked in this run stops the run, before
// its branch is touched.
func TestAnEarlierWorkerStillWorkingStopsTheRunBeforeItsWorktree(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.beads.add("A", "first", 1)
	h.herdr.agents = append(h.herdr.agents, &fakeAgent{name: "A", kind: "claude", pane: "elsewhere", status: "working"})
	o, code := h.run()
	if code != ExitTool || !strings.HasPrefix(o.Final(), "AGENT_BUSY: an earlier worker for A is still working in its tab") {
		t.Fatalf("exit %d, final %q", code, o.Final())
	}
	if exists(h.worktree("A")) {
		t.Error("a worktree was made under the earlier worker")
	}
}
