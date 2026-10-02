package dispatch

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// asksThenCarriesOn is a worker that asks the maintainer Q and ends its turn at its Stop hook, its
// ticket left in progress, and stays in its tab; once answered is closed it carries on there with
// then, as one answered in its tab does.
func asksThenCarriesOn(hooks *hookReporter, answered <-chan struct{}, then behaviour) behaviour {
	return func(w *fakeWorker) AgentState {
		hooks.report(w.wt, "PreToolUse")
		w.claim()
		w.ask("Q", "which way?")
		hooks.report(w.wt, "Stop")
		w.shows("idle")
		<-answered
		return then(w)
	}
}

// answersAfter is a worker that, after d, has the maintainer answer Q and closes answered; it then
// finishes its own ticket after a further while.
func answersAfter(d time.Duration, answered chan<- struct{}, while time.Duration, file string) behaviour {
	return func(w *fakeWorker) AgentState {
		time.Sleep(d)
		w.beads.set("Q", "closed")
		close(answered)
		time.Sleep(while)
		return finishes(file)(w)
	}
}

// busyFor is a worker that finishes its ticket after d.
func busyFor(d time.Duration, file string) behaviour {
	return func(w *fakeWorker) AgentState {
		time.Sleep(d)
		return finishes(file)(w)
	}
}

// closedIDs is the tickets merged, in order.
func closedIDs(h *harness) []string {
	var ids []string
	for _, ev := range h.sink.of(EvClosed) {
		id, _, _ := strings.Cut(ev, " ")
		ids = append(ids, id)
	}
	return ids
}

// tabOf is the tab ticket id's worker was started in, for a ticket given one tab: tickets
// dispatched side by side open theirs in either order.
func tabOf(h *harness, id string) string {
	h.herdr.mu.Lock()
	defer h.herdr.mu.Unlock()
	for _, p := range h.herdr.panes {
		if p.ticket == id {
			return p.tab
		}
	}
	return ""
}

// dispatches counts the times ticket id was dispatched.
func dispatches(h *harness, id string) int {
	n := 0
	for _, ev := range h.sink.of(EvDispatch) {
		if strings.HasPrefix(ev, id+" ") {
			n++
		}
	}
	return n
}

// The run of 2026-10-02: A asks; while both slots are busy the maintainer answers, and A's worker,
// told in its tab, claims A and closes it before a slot frees, so bd ready never lists it again.
// The run adopts it all the same and merges it, rebased onto the ticket merged meanwhile and
// checked, while the other two still run; when a slot frees, the next ready ticket takes it.
func TestAskedTicketClosedInItsTabWhileEverySlotIsBusyIsMerged(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.Concurrency = 2
		h.cfg.Check = "true"
		for i, id := range []string{"A", "B", "C", "D", "E"} {
			h.beads.add(id, "ticket "+id, i+1)
		}
		answered := make(chan struct{})
		h.worker("A", asksThenCarriesOn(hooks, answered, func(w *fakeWorker) AgentState {
			time.Sleep(time.Minute + 7*time.Second) // between two polls
			w.shows("working")
			hooks.report(w.wt, "PreToolUse")
			w.claim()
			time.Sleep(10 * time.Second)
			w.commit("a.txt")
			w.close()
			hooks.report(w.wt, "Stop")
			return "idle"
		}))
		h.worker("B", busyFor(time.Second, "b.txt")) // main moves on under A's branch, cut by then
		h.worker("C", answersAfter(5*time.Minute, answered, time.Hour, "c.txt"))
		h.worker("D", busyFor(time.Hour, "d.txt"))
		h.worker("E", finishes("e.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 5 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		tab := tabOf(h, "A")
		if got := h.sink.of(EvAnswered); !equal(got, []string{
			"A   ANSWERED: A was closed in tab " + tab + " after Q (which way?), so its worker is adopted"}) {
			t.Errorf("answered:\n%s", strings.Join(got, "\n"))
		}
		if got := closedIDs(h); len(got) != 5 || !equal(got[:2], []string{"B", "A"}) {
			t.Errorf("merged %v; want A merged after B, while C and D still ran\n%s", got, h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"A's worker in tab " + tab + " is idle with the ticket closed; adopting it",
			"rebased wt/A onto main, which moved on while it ran",
			"'true' passes on the rebased wt/A",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		if n := dispatches(h, "A"); n != 1 || len(h.herdr.argsFor("A")) != 1 {
			t.Errorf("A dispatched %d times, its worker started %d times; want once each", n, len(h.herdr.argsFor("A")))
		}
		if got := h.herdr.pastedTo(); len(got) != 0 {
			t.Errorf("pasted to %v; a worker that closed its ticket has nothing to be told", got)
		}
		if a := h.statusOf("A"); a != "closed" || !strings.Contains(h.mainLog(), "A: add a.txt") {
			t.Errorf("A is %s; main:\n%s", a, h.mainLog())
		}
	})
}

// An asked ticket whose worker, answered in its tab, has claimed it again and is still at work when
// the run notices is adopted at once, though every slot is busy, and watched like any running
// ticket: it settles at its own Stop hook and is merged.
func TestAskedTicketClaimedAgainInItsTabIsAdoptedAndWatched(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		answered := make(chan struct{})
		h.worker("A", asksThenCarriesOn(hooks, answered, func(w *fakeWorker) AgentState {
			time.Sleep(time.Minute)
			w.shows("working")
			hooks.report(w.wt, "PreToolUse")
			w.claim()
			time.Sleep(5 * time.Minute) // the run adopts it meanwhile
			w.commit("a.txt")
			w.close()
			hooks.report(w.wt, "Stop")
			return "idle"
		}))
		h.worker("B", answersAfter(time.Minute+7*time.Second, answered, time.Hour, "b.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.of(EvAnswered); !equal(got, []string{
			"A   ANSWERED: A was claimed again in tab tab1 after Q (which way?), so its worker is adopted"}) {
			t.Errorf("answered:\n%s", strings.Join(got, "\n"))
		}
		ev := h.sink.text()
		if !strings.Contains(ev, "A's worker in tab tab1 is working with the ticket in_progress; adopting it") ||
			!strings.Contains(ev, "A settled: Stop hook at ") {
			t.Errorf("A was not adopted and watched to its Stop:\n%s", ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A", "B"}) {
			t.Errorf("merged %v; want A merged while B still ran", got)
		}
		if n := dispatches(h, "A"); n != 1 {
			t.Errorf("A dispatched %d times", n)
		}
		if got := h.herdr.pastedTo(); len(got) != 0 {
			t.Errorf("pasted to %v; a working worker is left to work", got)
		}
	})
}

// An asked ticket claimed again whose worker is idle once its question is answered is adopted, and
// the worker told the answer is in, as when the ticket comes back through bd ready.
func TestAskedTicketClaimedAgainWithItsWorkerIdleIsResumed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		answered := make(chan struct{})
		h.worker("A",
			asksThenCarriesOn(hooks, answered, func(w *fakeWorker) AgentState {
				time.Sleep(time.Minute)
				w.claim()
				return "idle"
			}),
			finishes("a.txt")) // once told the answer is in
		h.worker("B", answersAfter(time.Minute+7*time.Second, answered, time.Hour, "b.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if ev := h.sink.text(); !strings.Contains(ev, "A's worker in tab tab1 was told Q is answered and carries on; adopting it") {
			t.Errorf("A's worker was not told:\n%s", ev)
		}
		if got := h.herdr.pastedTo(); !equal(got, []string{"A"}) {
			t.Errorf("pasted to %v; want the answer's news to A's worker", got)
		}
		if got := closedIDs(h); !equal(got, []string{"A", "B"}) {
			t.Errorf("merged %v", got)
		}
	})
}

// An asked ticket its worker defers in its tab shows as deferred, without coming back.
func TestAskedTicketDeferredInItsTabIsSetAsideAsDeferred(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		told := make(chan struct{})
		h.worker("A", asksThenCarriesOn(hooks, told, func(w *fakeWorker) AgentState {
			w.deferIt()
			return "idle"
		}))
		h.worker("B", func(w *fakeWorker) AgentState {
			time.Sleep(time.Minute)
			close(told) // the maintainer, in A's tab, has it deferred; Q stays open
			return busyFor(time.Hour, "b.txt")(w)
		})
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.of(EvDeferred); len(got) != 1 ||
			!strings.HasPrefix(got[0], "A   A deferred by worker after its question Q; worktree ") ||
			!strings.HasSuffix(got[0], " and tab tab1 left open") {
			t.Errorf("deferred:\n%s", strings.Join(got, "\n"))
		}
		if len(h.sink.of(EvAnswered)) != 0 || dispatches(h, "A") != 1 {
			t.Errorf("A came back:\n%s", h.sink.text())
		}
		if a := h.statusOf("A"); a != "deferred" {
			t.Errorf("A is %s", a)
		}
	})
}

// An asked ticket claimed again whose worker is then gone from its tab stops the run, as a paused
// ticket does, rather than being waited on forever; it is noted, and labelled as it may yet close.
func TestAskedTicketWhoseWorkerIsGoneStopsTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		answered := make(chan struct{})
		h.worker("A", asksThenCarriesOn(hooks, answered, func(w *fakeWorker) AgentState {
			time.Sleep(time.Minute)
			w.claim()
			return StateGone // its tab closed by hand, say
		}))
		h.worker("B", answersAfter(time.Minute+7*time.Second, answered, time.Hour, "b.txt"))
		o, code := h.run()
		want := "PAUSED: A still in_progress after its question Q, its worker gone from tab tab1 (worktree " +
			h.worktree("A") + "); stopping so it can be looked at"
		if code != ExitStuck || o.Final() != want {
			t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), want, h.sink.text())
		}
		if got := h.sink.of(EvHold); len(got) != 1 || !strings.HasPrefix(got[0], "A HOLD: "+want+"; no new tickets while the 1 running finish") {
			t.Errorf("holds:\n%s", strings.Join(got, "\n"))
		}
		if got := closedIDs(h); !equal(got, []string{"B"}) {
			t.Errorf("merged %v; want B, running, merged", got)
		}
		if notes := h.beads.notesOf("A"); !strings.Contains(notes, "the worker in Herdr tab tab1 is gone, with the ticket still in_progress") {
			t.Errorf("notes on A: %q", notes)
		}
		if a, _ := h.beads.Show(t.Context(), "A"); !HasLabel(a, UnmergedLabel) {
			t.Errorf("A should be labelled %q: %v", UnmergedLabel, a.Labels)
		}
	})
}

// An asked ticket its worker closes while the run holds for a failure is not merged: as the run
// ends it is left for review and labelled, so the tickets it blocks wait for it in later runs.
func TestAskedTicketClosedWhileTheRunHoldsIsLabelled(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.Concurrency = 3
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.add("C", "third", 3)
		h.worker("A", asksThenCarriesOn(hooks, h.sink.held, func(w *fakeWorker) AgentState {
			w.commit("a.txt")
			w.close()
			return "idle"
		}))
		h.worker("B", func(w *fakeWorker) AgentState { w.claim(); return "idle" }) // pauses the run
		h.worker("C", busyFor(time.Hour, "c.txt"))
		o, code := h.run()
		if code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: B still in_progress") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if len(h.sink.of(EvAnswered)) != 0 || strings.Contains(h.mainLog(), "A: add a.txt") {
			t.Errorf("A was adopted while the run held:\n%s", h.sink.text())
		}
		if a, _ := h.beads.Show(t.Context(), "A"); !HasLabel(a, UnmergedLabel) {
			t.Errorf("A should be labelled %q: %v", UnmergedLabel, a.Labels)
		}
		warned := slices.IndexFunc(h.sink.events, func(ev Event) bool {
			return ev.Kind == EvWarn && ev.Ticket == "A" && ev.Aside && strings.HasPrefix(ev.Text,
				"  ASKED_UNMERGED: A was closed in tab "+tabOf(h, "A")+" after its question Q, and the run ends before merging it")
		})
		if stopped := slices.IndexFunc(h.sink.events, func(ev Event) bool { return ev.Kind == EvStop }); warned < 0 || warned > stopped {
			t.Errorf("A should be left for review before the final line:\n%s", h.sink.text())
		}
	})
}

// At the ticket limit an asked ticket closed in its tab is still adopted and merged before the run
// ends; one claimed again and still at work is left to its worker, labelled.
func TestAskedTicketsAtTheTicketLimit(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		closes bool
	}{{"closed", true}, {"in progress", false}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				hooks := &hookReporter{}
				h.reporter = hooks
				h.cfg.Limit = 2
				h.cfg.Check = "true"
				h.beads.add("A", "first", 1)
				h.beads.add("B", "second", 2)
				answered, carried := make(chan struct{}), make(chan struct{})
				h.worker("A", asksThenCarriesOn(hooks, answered, func(w *fakeWorker) AgentState {
					w.claim()
					if c.closes {
						w.commit("a.txt")
						w.close()
						close(carried)
						return "idle"
					}
					w.shows("working")
					close(carried)
					time.Sleep(time.Hour)
					return "idle"
				}))
				h.worker("B", func(w *fakeWorker) AgentState {
					w.beads.set("Q", "closed")
					close(answered)
					<-carried // B ends after A's worker carried on, between two polls
					return finishes("b.txt")(w)
				})
				o, code := h.run()
				if code != ExitOK || o.Final() != "LIMIT_REACHED at 2 tickets" {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				a, _ := h.beads.Show(t.Context(), "A")
				merged := strings.Contains(h.mainLog(), "A: add a.txt")
				if c.closes {
					if !merged || HasLabel(a, UnmergedLabel) || !equal(closedIDs(h), []string{"B", "A"}) {
						t.Errorf("A should be merged after B, unlabelled (%v):\n%s", a.Labels, h.sink.text())
					}
					return
				}
				if merged || len(h.sink.of(EvAnswered)) != 0 || !HasLabel(a, UnmergedLabel) {
					t.Errorf("A should be left to its worker, labelled (%v):\n%s", a.Labels, h.sink.text())
				}
				if ev := h.sink.text(); !strings.Contains(ev, "A is in progress after its question (tab tab1) and labelled 'unmerged'") {
					t.Errorf("events:\n%s", ev)
				}
			})
		})
	}
}
