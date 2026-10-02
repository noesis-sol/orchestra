package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// Whole runs, two or more in a row on the same fakes, as a maintainer runs orchestra again: the
// workers one run leaves behind, on tickets asked or left running, are carried over to the next.

// saved is the workers the last run left behind, as its state file has them.
func saved(t *testing.T, h *harness) []project.LeftWorker {
	t.Helper()
	s, err := project.LoadState(h.repo)
	if err != nil {
		t.Fatal(err)
	}
	return s.Workers
}

// warnings is the warnings about ticket id, across the runs so far.
func warnings(h *harness, id string) []string {
	var l []string
	for _, ev := range h.sink.of(EvWarn) {
		if strings.HasPrefix(ev, id+" ") {
			l = append(l, ev)
		}
	}
	return l
}

// orchestra-5pj on 2026-10-02: A asks; the run ends with A waiting on its question. The maintainer
// answers in A's tab, and its worker carries on there, commits and closes A after the run. The next
// run merges it, rebased onto what landed on main meanwhile and checked, and then starts the ticket
// it blocks.
func TestAskedTicketClosedBetweenRunsIsMergedByTheNext(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.Check = "true"
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.link("B", "A", "blocks")
		answered := make(chan struct{})
		h.worker("A", asksThenCarriesOn(hooks, answered, func(w *fakeWorker) AgentState {
			w.claim()
			w.commit("a.txt")
			w.close()
			hooks.report(w.wt, "Stop")
			return "idle"
		}))
		h.worker("B", finishes("b.txt"))
		if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := []project.LeftWorker{{Ticket: "A", Agent: "A", Tab: "tab1", Worktree: h.worktree("A"), Hooks: true,
			Question: "Q", QuestionTitle: "which way?"}}
		if got := saved(t, h); !reflect.DeepEqual(got, want) {
			t.Fatalf("saved %+v\nwant %+v", got, want)
		}
		if ev := h.sink.text(); !strings.Contains(ev, "  left for the next run: A (asked Q)\nREADY_EMPTY") {
			t.Errorf("run 1 should say what it leaves for the next, before its final line:\n%s", ev)
		}

		// Between runs: answered in A's tab, its worker finishes A; a commit lands on main by hand.
		h.beads.set("Q", "closed")
		close(answered)
		synctest.Wait()
		h.mem.commit("main", "by hand", "x.txt")

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"  carried over from the last run: A (asked Q)",
			"  ANSWERED: A was closed in tab tab1 after Q (which way?), so its worker is adopted",
			"  rebased wt/A onto main, which moved on while it ran",
			"  'true' passes on the rebased wt/A",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("run 2 events lack %q:\n%s", want, ev)
			}
		}
		if got := closedIDs(h); !equal(got, []string{"A", "B"}) {
			t.Errorf("merged %v; want A, then B, which it blocks", got)
		}
		if n := dispatches(h, "A"); n != 1 || len(h.herdr.argsFor("A")) != 1 {
			t.Errorf("A dispatched %d times, its worker started %d times; want once each", n, len(h.herdr.argsFor("A")))
		}
		if got := saved(t, h); len(got) != 0 {
			t.Errorf("run 2 left %+v; want nothing", got)
		}
		if _, err := os.Stat(filepath.Join(h.repo, project.RunPath(project.StateName))); !os.IsNotExist(err) {
			t.Errorf("the state file should be gone with nothing in it: %v", err)
		}
	})
}

// A ticket left running when the run stopped, which its worker closed after the run, is merged by
// the next run, which removes its unmerged label and then starts the ticket it blocks.
func TestTicketLeftRunningAndClosedBetweenRunsIsMergedByTheNext(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Check = "true"
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.link("B", "A", "blocks")
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
		h.worker("B", finishes("b.txt"))
		if o, code := h.run(); code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: A still in_progress") {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := []project.LeftWorker{{Ticket: "A", Agent: "A", Tab: "tab1", Worktree: h.worktree("A"), Left: "PAUSED"}}
		if got := saved(t, h); !reflect.DeepEqual(got, want) {
			t.Fatalf("saved %+v\nwant %+v", got, want)
		}

		// Answered in its tab, the worker commits and closes A after the run; main moves on.
		if err := h.mem.commitIn(h.worktree("A"), "A: add a.txt", "a.txt"); err != nil {
			t.Fatal(err)
		}
		h.beads.set("A", "closed")
		h.mem.commit("main", "by hand", "x.txt")

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"  carried over from the last run: A (left running: PAUSED)",
			"  A, which the last run left running (PAUSED), is closed in tab tab1, so its worker is adopted",
			"  'true' passes on the rebased wt/A",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("run 2 events lack %q:\n%s", want, ev)
			}
		}
		if got := closedIDs(h); !equal(got, []string{"A", "B"}) {
			t.Errorf("merged %v; want A, then B", got)
		}
		if a, _ := h.beads.Show(t.Context(), "A"); HasLabel(a, UnmergedLabel) {
			t.Errorf("A merged, so its label should be removed: %v", a.Labels)
		}
		if len(h.sink.of(EvAnswered)) != 0 {
			t.Errorf("A asked nothing, so nothing was answered:\n%s", ev)
		}
	})
}

// An asked ticket answered in its tab between runs, whose worker is at work on it again when the
// next run starts, is adopted by that run and watched to its end, rather than stopping it with
// AGENT_BUSY: whether the worker has claimed the ticket again (bd ready doesn't list it) or not
// yet (it comes back through bd ready).
func TestAskedTicketAtWorkAgainWhenTheNextRunStartsIsAdopted(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		claimed bool
		adopted string
	}{
		{"claimed again", true, "  A's worker in tab tab1 is working with the ticket in_progress; adopting it"},
		{"not claimed yet", false, "  A's earlier worker is still working in tab tab1; adopting it rather than starting another"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				hooks := &hookReporter{}
				h.reporter = hooks
				h.beads.add("A", "first", 1)
				answered := make(chan struct{})
				h.worker("A", asksThenCarriesOn(hooks, answered, func(w *fakeWorker) AgentState {
					w.shows("working")
					hooks.report(w.wt, "PreToolUse")
					if c.claimed {
						w.claim()
					}
					time.Sleep(5 * time.Minute) // the next run starts meanwhile
					w.claim()
					w.commit("a.txt")
					w.close()
					hooks.report(w.wt, "Stop")
					return "idle"
				}))
				if o, code := h.run(); code != ExitOK {
					t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				h.beads.set("Q", "closed")
				close(answered)
				synctest.Wait() // the worker is at work again

				o, code := h.run()
				if code != ExitOK || !strings.HasPrefix(o.Final(), "READY_EMPTY after ") {
					t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				ev := h.sink.text()
				if !strings.Contains(ev, c.adopted) || !strings.Contains(ev, "A settled: Stop hook at ") {
					t.Errorf("A's worker should be adopted and watched to its Stop:\n%s", ev)
				}
				if strings.Contains(ev, string(stopAgentBusy)) {
					t.Errorf("run 2 stopped for A's earlier worker:\n%s", ev)
				}
				if got := closedIDs(h); !equal(got, []string{"A"}) || len(h.herdr.argsFor("A")) != 1 {
					t.Errorf("merged %v, A's worker started %d times; want A merged from its one worker",
						got, len(h.herdr.argsFor("A")))
				}
				if got := h.herdr.pastedTo(); len(got) != 0 {
					t.Errorf("pasted to %v; a working worker is left to work", got)
				}
			})
		})
	}
}

// A ticket left running past the ticket limit, whose worker is still at work when the next run
// starts, is adopted by that run and watched to its end, though bd ready doesn't list it.
func TestTicketLeftRunningStillAtWorkIsAdoptedByTheNextRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.TicketLimit = 30 * time.Minute
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			time.Sleep(45 * time.Minute) // past the limit, and well within it again once adopted
			return finishes("a.txt")(w)
		})
		if o, code := h.run(); code != ExitStuck || !strings.HasPrefix(o.Final(), "TICKET_LIMIT: A still working") {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"  carried over from the last run: A (left running: TICKET_LIMIT)",
			"  A, which the last run left running (TICKET_LIMIT), is at work again in tab tab1, so its worker is adopted",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("run 2 events lack %q:\n%s", want, ev)
			}
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) || len(h.herdr.argsFor("A")) != 1 {
			t.Errorf("merged %v, A's worker started %d times; want A merged from its one worker",
				got, len(h.herdr.argsFor("A")))
		}
	})
}

// An asked ticket whose question is answered between runs comes back through bd ready in the next
// one, and its earlier worker, idle in its tab, is told the answer is in and carries on with what
// it knows, rather than being renamed for a new worker to start over.
func TestAskedTicketAnsweredBetweenRunsResumesItsWorker(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", asksAndStops(hooks.report), finishes("a.txt"))
		if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		h.beads.set("Q", "closed") // bd human respond Q

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"  ANSWERED: Q (which way?) is answered, so A comes back",
			"  A's earlier worker in tab tab1 was told Q is answered and carries on; adopting it",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("run 2 events lack %q:\n%s", want, ev)
			}
		}
		if strings.Contains(ev, "renamed") {
			t.Errorf("A's earlier worker was renamed:\n%s", ev)
		}
		if got := h.herdr.pastedTo(); !equal(got, []string{"A"}) || len(h.herdr.argsFor("A")) != 1 {
			t.Errorf("pasted to %v, A's worker started %d times; want the answer's news to A's one worker",
				got, len(h.herdr.argsFor("A")))
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v", got)
		}
	})
}

// An asked ticket whose question is still open when the next run ends is kept for the run after,
// which resumes its worker once the question is answered.
func TestAskedTicketStillWaitingIsKeptForTheRunAfter(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", asksAndStops(hooks.report), finishes("a.txt"))
		if _, code := h.run(); code != ExitOK {
			t.Fatalf("run 1: exit %d\n%s", code, h.sink.text())
		}
		first := saved(t, h)

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := saved(t, h); len(got) != 1 || !reflect.DeepEqual(got, first) {
			t.Errorf("run 2 saved %+v; want A kept as run 1 left it, %+v", got, first)
		}
		if ev := h.sink.text(); strings.Count(ev, "  left for the next run: A (asked Q)") != 2 {
			t.Errorf("both runs should leave A for the next:\n%s", ev)
		}

		h.beads.set("Q", "closed")
		if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 3: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) || !equal(h.herdr.pastedTo(), []string{"A"}) {
			t.Errorf("merged %v, pasted to %v; want A's worker resumed and A merged", got, h.herdr.pastedTo())
		}
	})
}

// An asked ticket its worker deferred between runs is set aside as deferred by the next run.
func TestAskedTicketDeferredBetweenRunsIsSetAside(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", asksAndStops(hooks.report))
		if _, code := h.run(); code != ExitOK {
			t.Fatalf("run 1: exit %d\n%s", code, h.sink.text())
		}
		h.beads.set("A", "deferred") // told in its tab that it can't go on

		if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := "A   A deferred by worker after its question Q; worktree " + h.worktree("A") + " and tab tab1 left open"
		if got := h.sink.of(EvDeferred); !equal(got, []string{want}) {
			t.Errorf("deferred:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
		}
		if got := saved(t, h); len(got) != 0 {
			t.Errorf("run 2 left %+v; want nothing", got)
		}
	})
}

// A carried-over worker whose worktree is gone, or whose ticket was merged by hand, is dropped,
// saying why, and the next run neither adopts it nor keeps it.
func TestCarriedOverWorkerNoLongerThereIsDropped(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		between func(t *testing.T, h *harness)
		why     func(h *harness) string
	}{
		{
			"worktree gone",
			func(t *testing.T, h *harness) {
				if _, err := h.mem.RemoveWorktree(t.Context(), h.repo, h.worktree("A")); err != nil {
					t.Fatal(err)
				}
			},
			func(h *harness) string { return "its worktree " + h.worktree("A") + " is gone" },
		},
		{
			"merged by hand",
			func(t *testing.T, h *harness) {
				if _, err := h.mem.FastForward(t.Context(), h.repo, "wt/A"); err != nil {
					t.Fatal(err)
				}
				h.beads.set("Q", "closed")
				h.beads.set("A", "closed")
			},
			func(h *harness) string {
				return "wt/A is on main already (" + h.mem.CommitNamingOn(context.Background(), h.repo, "main", "A") + ")"
			},
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				hooks := &hookReporter{}
				h.reporter = hooks
				h.beads.add("A", "first", 1)
				h.worker("A", func(w *fakeWorker) AgentState {
					w.commit("a.txt")
					return asksAndStops(hooks.report)(w)
				})
				if _, code := h.run(); code != ExitOK || len(saved(t, h)) != 1 {
					t.Fatalf("run 1: exit %d, saved %+v\n%s", code, saved(t, h), h.sink.text())
				}
				c.between(t, h)

				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
					t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				ev := h.sink.text()
				if want := "  A, carried over from the last run, is dropped: " + c.why(h); !strings.Contains(ev, want) {
					t.Errorf("run 2 events lack %q:\n%s", want, ev)
				}
				if strings.Contains(ev, "carried over from the last run: A") || len(h.sink.of(EvAnswered)) != 0 {
					t.Errorf("A was carried over:\n%s", ev)
				}
				if got := saved(t, h); len(got) != 0 {
					t.Errorf("run 2 left %+v; want nothing", got)
				}
			})
		})
	}
}

// A ticket left running whose worker is gone by the next run, its ticket still in progress, is
// warned about once and noted, and the next run goes on with the other tickets rather than stopping
// for it; the run after says nothing more of it.
func TestTicketLeftRunningWhoseWorkerIsGoneIsWarnedOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.beads.add("C", "third", 3)
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
		h.worker("C", finishes("c.txt"))
		if o, code := h.run(); code != ExitStuck {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if err := h.herdr.CloseTab(t.Context(), "tab1"); err != nil { // closed by hand
			t.Fatal(err)
		}

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := "A   WORKER_GONE: A is in progress after the last run left it running (PAUSED), " +
			"but its worker is gone from tab tab1 (worktree " + h.worktree("A") + "), so it isn't carried over; " +
			"reopen it to run it again (bd update A --status open), or finish it by hand"
		if got := warnings(h, "A"); !equal(got, []string{want}) {
			t.Errorf("warnings:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
		}
		if notes := h.beads.notesOf("A"); !strings.Contains(notes, "the worker in Herdr tab tab1 is gone, "+
			"with the ticket still in_progress after the last run left it running (PAUSED)") {
			t.Errorf("notes on A: %q", notes)
		}
		if got := closedIDs(h); !equal(got, []string{"C"}) {
			t.Errorf("merged %v; want C", got)
		}

		if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("run 3: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := warnings(h, "A"); len(got) != 1 {
			t.Errorf("A should be warned about once:\n%s", strings.Join(got, "\n"))
		}
	})
}

// Ctrl+C leaves the running worker, saved for the next run as left running, INTERRUPTED; it closes
// its ticket after the run, and the next run merges it. It is saved wherever its start had got to:
// Ctrl+C as the worker starts on its prompt, given at launch, may land before the run has heard from
// Herdr that it did, or while Herdr is still to name it.
func TestInterruptedRunLeavesItsWorkerToTheNext(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		naming bool // Ctrl+C lands as Herdr begins to name the worker, rather than as it starts work
	}{
		{"as the worker starts", false},
		{"while Herdr names it", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.beads.add("A", "first", 1)
				started, release := make(chan struct{}), make(chan struct{})
				h.worker("A", func(w *fakeWorker) AgentState {
					w.claim()
					close(started)
					<-release
					w.commit("a.txt")
					w.close()
					return "idle"
				})
				o := h.loop()
				ctx, cancel := context.WithCancelCause(t.Context())
				interrupt := func() { cancel(InterruptedError("with Ctrl+C")) }
				if c.naming {
					h.herdr.onAdopt = func(string) { interrupt() }
				}
				codes := make(chan int, 1)
				go func() { codes <- o.Run(ctx) }()
				if !c.naming {
					<-started
					interrupt()
				}
				if code := <-codes; code != ExitInterrupted {
					t.Fatalf("run 1: exit %d, final %q", code, o.Final())
				}
				want := []project.LeftWorker{
					{Ticket: "A", Agent: "A", Tab: "tab1", Worktree: h.worktree("A"), Left: "INTERRUPTED"}}
				if got := saved(t, h); !reflect.DeepEqual(got, want) {
					t.Fatalf("saved %+v\nwant %+v", got, want)
				}
				close(release)
				synctest.Wait()

				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
					t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if ev := h.sink.text(); !strings.Contains(ev, "  carried over from the last run: A (left running: INTERRUPTED)") {
					t.Errorf("events:\n%s", ev)
				}
				if a, _ := h.beads.Show(t.Context(), "A"); !equal(closedIDs(h), []string{"A"}) || HasLabel(a, UnmergedLabel) {
					t.Errorf("A should be merged and its label removed (%v):\n%s", a.Labels, h.sink.text())
				}
			})
		})
	}
}

// A scoped run carries over only the workers on its own tickets; it keeps the others, as they
// were, for a later run.
func TestScopedRunKeepsOtherWorkersForALaterRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.worker("A", asksAndStops(hooks.report))
		if _, code := h.run(); code != ExitOK {
			t.Fatalf("run 1: exit %d\n%s", code, h.sink.text())
		}
		first := saved(t, h)
		h.beads.add("R", "scope", 2)
		h.worker("R", finishes("r.txt"))
		h.beads.set("A", "closed") // as if its worker had closed it: still not this run's

		h.cfg.Ticket = "R"
		if _, code := h.run(); code != ExitOK {
			t.Fatalf("run 2: exit %d\n%s", code, h.sink.text())
		}
		if ev := h.sink.text(); !strings.Contains(ev, "  kept for a later run, outside this run's scope: A (asked Q)") {
			t.Errorf("events:\n%s", ev)
		}
		if got := closedIDs(h); !equal(got, []string{"R"}) {
			t.Errorf("merged %v; want only R", got)
		}
		if got := saved(t, h); !reflect.DeepEqual(got, first) {
			t.Errorf("run 2 saved %+v; want A kept as run 1 left it, %+v", got, first)
		}
	})
}

// A state file that can't be read carries nothing over; the run says so in its log and goes on.
func TestUnreadableStateFileCarriesNothingOver(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		path := filepath.Join(h.repo, project.RunPath(project.StateName))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if logged := h.logged(); !strings.Contains(logged,
			"cannot read the workers the last run left behind, so none is carried over: "+project.RunPath(project.StateName)) {
			t.Errorf("log:\n%s", logged)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("with nothing left behind the file should be gone: %v", err)
		}
	})
}
