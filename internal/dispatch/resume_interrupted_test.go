package dispatch

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// answeredRun is a run in which A asks Q and is set aside, B answers Q meanwhile and is merged, and
// A comes back through bd ready: its earlier worker, idle in tab1, is told the answer is in, and
// carries on with then.
func answeredRun(t *testing.T, then behaviour) *harness {
	h := newTimedHarness(t)
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A", func(w *fakeWorker) AgentState { w.claim(); w.ask("Q", "which way?"); return "idle" }, then)
	h.worker("B", func(w *fakeWorker) AgentState {
		w.beads.set("Q", "closed") // the maintainer answers meanwhile
		return finishes("b.txt")(w)
	})
	return h
}

// cutSink is the run's sink, calling on with each event as the run emits it, where a test presses
// Ctrl+C.
type cutSink struct {
	*runSink
	on func(Event)
}

func (s cutSink) Event(ev Event) {
	s.runSink.Event(ev)
	s.on(ev)
}

// cutAtStatus is Herdr's agents, pressing Ctrl+C (cut) as the run reads ticket id's worker's state,
// once armed.
type cutAtStatus struct {
	Agents
	id    string
	armed *atomic.Bool
	cut   func()
}

func (a cutAtStatus) Status(ctx context.Context, name string) (AgentState, error) {
	if name == a.id && a.armed.Load() {
		a.cut()
	}
	return a.Agents.Status(ctx, name)
}

// Ctrl+C while A's earlier worker is told its question is answered, Herdr still to see it start:
// the worker has the message, and may claim and close A once orchestra has gone. A is left running,
// as one adopted is: the INTERRUPTED line names it, it is labelled, and it is carried over, so the
// next run merges it once its worker has closed it.
func TestCtrlCAsAnAskedTicketsWorkerIsToldLeavesItRunning(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		free := sync.OnceFunc(func() { close(release) })
		defer free()
		h := answeredRun(t, func(w *fakeWorker) AgentState { <-release; return finishes("a.txt")(w) })
		h.herdr.promptTakes = time.Minute // Herdr waits to see the worker start on it
		o := h.loop()
		o.ReportInterrupt = true
		ctx, cancel := context.WithCancelCause(t.Context())
		h.herdr.onPrompt = func(id string) {
			if id == "A" {
				cancel(InterruptedError("with Ctrl+C"))
			}
		}
		if code := o.Run(ctx); code != ExitInterrupted {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.herdr.pastedTo(); !equal(got, []string{"A"}) {
			t.Fatalf("pasted to %v; want the answer's news to A's worker", got)
		}
		interrupted := "INTERRUPTED: stopped with Ctrl+C while A (tab tab1) were running; " +
			"their tabs and worktrees are left open"
		if o.Final() != interrupted {
			t.Errorf("final %q, want %q", o.Final(), interrupted)
		}
		if ev := h.sink.text(); !before(ev, leftRunning("A", "tab1"), interrupted) {
			t.Errorf("A's label should be said before the INTERRUPTED line; events:\n%s", ev)
		}
		if a, _ := h.beads.Show(t.Context(), "A"); !HasLabel(a, UnmergedLabel) {
			t.Errorf("A should be labelled %q: %v", UnmergedLabel, a.Labels)
		}
		want := []project.LeftWorker{
			{Ticket: "A", Agent: "A", Tab: "tab1", Worktree: h.worktree("A"), Left: "INTERRUPTED"}}
		if got := saved(t, h); !reflect.DeepEqual(got, want) {
			t.Errorf("saved %+v\nwant %+v", got, want)
		}

		// Told, A's worker carries on after the run, and closes A.
		free()
		synctest.Wait()
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"  carried over from the last run: A (left running: INTERRUPTED)",
			"  A, which the last run left running (INTERRUPTED), is closed in tab tab1, so its worker is adopted",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("run 2 events lack %q:\n%s", want, ev)
			}
		}
		if a, _ := h.beads.Show(t.Context(), "A"); !equal(closedIDs(h), []string{"B", "A"}) || HasLabel(a, UnmergedLabel) {
			t.Errorf("merged %v; A should be merged after B and its label removed (%v):\n%s",
				closedIDs(h), a.Labels, ev)
		}
		if n := len(h.herdr.argsFor("A")); n != 1 {
			t.Errorf("A's worker started %d times; want A merged from its one worker", n)
		}
	})
}

// Ctrl+C as an answered ticket comes back, before its earlier worker is told anything, leaves it
// asked: it is saved with its question, and the next run tells that worker the answer is in.
func TestCtrlCBeforeAnAskedTicketsWorkerIsToldLeavesItAsked(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name   string
		status bool // Ctrl+C lands as the run reads the earlier worker's state, rather than as A comes back
	}{
		{"as it comes back", false},
		{"as its earlier worker is looked at", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := answeredRun(t, finishes("a.txt"))
				o := h.loop()
				o.ReportInterrupt = true
				ctx, cancel := context.WithCancelCause(t.Context())
				interrupt := func() { cancel(InterruptedError("with Ctrl+C")) }
				var armed atomic.Bool
				if c.status {
					o.agents = cutAtStatus{Agents: o.agents, id: "A", armed: &armed, cut: interrupt}
				}
				o.SetSink(cutSink{runSink: h.sink, on: func(ev Event) {
					if ev.Kind != EvAnswered || ev.Ticket != "A" {
						return
					}
					if c.status {
						armed.Store(true)
					} else {
						interrupt()
					}
				}})
				if code := o.Run(ctx); code != ExitInterrupted {
					t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if got := h.herdr.pastedTo(); len(got) != 0 {
					t.Fatalf("pasted to %v; A's worker should not have been told anything", got)
				}
				if c.status && !armed.Load() {
					t.Fatal("Ctrl+C was never pressed")
				}
				want := []project.LeftWorker{
					{Ticket: "A", Agent: "A", Tab: "tab1", Worktree: h.worktree("A"), Question: "Q", QuestionTitle: "which way?"}}
				if got := saved(t, h); !reflect.DeepEqual(got, want) {
					t.Errorf("saved %+v\nwant %+v", got, want)
				}

				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
					t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				ev := h.sink.text()
				for _, want := range []string{
					"  carried over from the last run: A (asked Q)",
					"  A's earlier worker in tab tab1 was told Q is answered and carries on; adopting it",
				} {
					if !strings.Contains(ev, want) {
						t.Errorf("run 2 events lack %q:\n%s", want, ev)
					}
				}
				if strings.Contains(ev, "renamed") {
					t.Errorf("A's earlier worker was renamed:\n%s", ev)
				}
				if !equal(closedIDs(h), []string{"B", "A"}) || len(h.herdr.argsFor("A")) != 1 {
					t.Errorf("merged %v, A's worker started %d times; want A merged from its one worker after B",
						closedIDs(h), len(h.herdr.argsFor("A")))
				}
			})
		})
	}
}

// The same Ctrl+C as a ticket the last run left asked, claimed again between the runs, has its
// idle worker told the answer is in (see adoptAsked): A is left running and carried over too.
func TestCtrlCAsACarriedOverWorkerIsToldLeavesItRunning(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		free := sync.OnceFunc(func() { close(release) })
		defer free()
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A",
			func(w *fakeWorker) AgentState { w.claim(); w.ask("Q", "which way?"); return "idle" },
			func(w *fakeWorker) AgentState { <-release; return finishes("a.txt")(w) })
		if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		h.beads.set("Q", "closed")      // answered
		h.beads.set("A", "in_progress") // and claimed again in its tab, its worker then idle

		h.herdr.promptTakes = time.Minute
		o := h.loop()
		o.ReportInterrupt = true
		ctx, cancel := context.WithCancelCause(t.Context())
		h.herdr.onPrompt = func(id string) {
			if id == "A" {
				cancel(InterruptedError("with Ctrl+C"))
			}
		}
		if code := o.Run(ctx); code != ExitInterrupted {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if !strings.HasPrefix(o.Final(), "INTERRUPTED: stopped with Ctrl+C while A (tab tab1) were running") {
			t.Errorf("final %q", o.Final())
		}
		want := []project.LeftWorker{
			{Ticket: "A", Agent: "A", Tab: "tab1", Worktree: h.worktree("A"), Left: "INTERRUPTED"}}
		if got := saved(t, h); !reflect.DeepEqual(got, want) {
			t.Errorf("saved %+v\nwant %+v", got, want)
		}

		free()
		synctest.Wait()
		if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("run 3: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if a, _ := h.beads.Show(t.Context(), "A"); !equal(closedIDs(h), []string{"A"}) || HasLabel(a, UnmergedLabel) {
			t.Errorf("A should be merged and its label removed (%v):\n%s", a.Labels, h.sink.text())
		}
	})
}
