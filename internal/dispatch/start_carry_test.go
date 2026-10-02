package dispatch

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A worker's start cut short, by Ctrl+C or a failure, once its tab is open: a worker may be at work
// in the tab, so the run saves it for the next run. Ctrl+C before Herdr named the worker it launched
// with its prompt names it, if Herdr has it.

// runCutAtNaming runs the loop, pressing Ctrl+C as Herdr begins to name the worker it launched.
func runCutAtNaming(t *testing.T, h *harness) (*Loop, int) {
	o := h.loop()
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	h.herdr.mu.Lock()
	h.herdr.onAdopt = func(string) { cancel(InterruptedError("with Ctrl+C")) }
	h.herdr.mu.Unlock()
	code := o.Run(ctx)
	h.herdr.mu.Lock()
	h.herdr.onAdopt = nil
	h.herdr.mu.Unlock()
	return o, code
}

// savedCut is what a run cut short as A's worker was being named saves for the next one.
func savedCut(h *harness) []project.LeftWorker {
	return []project.LeftWorker{{Ticket: "A", Agent: "A", Tab: "tab1", Pane: "pane1", Worktree: h.worktree("A"),
		Left: "INTERRUPTED"}}
}

// The worker is still at work when the next run starts: the run cut short named it as it stopped,
// so the next one finds it under that name, adopts it rather than starting another, and merges its
// ticket once it closes it.
func TestWorkerCutShortBeforeItIsNamedIsAdoptedByTheNextRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		claimed := make(chan struct{})
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			close(claimed)
			time.Sleep(10 * time.Minute) // still at work when the next run starts
			return finishes("a.txt")(w)
		})
		if o, code := runCutAtNaming(t, h); code != ExitInterrupted {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got, want := saved(t, h), savedCut(h); !reflect.DeepEqual(got, want) {
			t.Fatalf("saved %+v\nwant %+v", got, want)
		}
		<-claimed // in progress, as the next run finds it

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"  carried over from the last run: A (left running: INTERRUPTED)",
			"  A, which the last run left running (INTERRUPTED), is at work again in tab tab1, so its worker is adopted",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("run 2 events lack %q:\n%s", want, ev)
			}
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) || len(h.herdr.argsFor("A")) != 1 {
			t.Errorf("merged %v, A's worker started %d times; want A merged from its one worker",
				got, len(h.herdr.argsFor("A")))
		}
		if got := warnings(h, "A"); len(got) != 0 {
			t.Errorf("A warned about:\n%s", strings.Join(got, "\n"))
		}
	})
}

// No worker ever came up in the tab: the next run, finding none under the worker's name, drops what
// was saved and starts one on the ticket as it comes back through bd ready.
func TestWorkerCutShortBeforeItCameUpIsStartedAgainByTheNextRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.herdr.launchLost["A"] = true
		h.worker("A", finishes("a.txt"))
		if o, code := runCutAtNaming(t, h); code != ExitInterrupted {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got, want := saved(t, h), savedCut(h); !reflect.DeepEqual(got, want) {
			t.Fatalf("saved %+v\nwant %+v", got, want)
		}
		h.herdr.mu.Lock()
		delete(h.herdr.launchLost, "A")
		h.herdr.mu.Unlock()

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) || len(h.herdr.argsFor("A")) != 2 {
			t.Errorf("merged %v, A's worker launched %d times; want A merged from a second launch",
				got, len(h.herdr.argsFor("A")))
		}
		if a, _ := h.beads.Show(t.Context(), "A"); HasLabel(a, UnmergedLabel) {
			t.Errorf("A keeps its %s label:\n%s", UnmergedLabel, h.sink.text())
		}
		if got := saved(t, h); len(got) != 0 {
			t.Errorf("run 2 saved %+v; want nothing left", got)
		}
	})
}

// A start that fails once the worker's tab is open stops the run with the worker saved, as one may
// be in the tab; the next run, finding none under its name, starts one on the ticket as it comes
// back through bd ready.
func TestFailedStartLeavesItsWorkerToTheNext(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.LaunchPrompt, h.cfg.WorkerArgs = false, []string{"--no-chrome"}
		h.herdr.refuseArgs = true // refuses even the arguments every worker gets, on every try
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		if o, code := h.run(); code != ExitTool || !strings.HasPrefix(o.Final(), "START_FAILED for A in tab tab1") {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := []project.LeftWorker{{Ticket: "A", Agent: "A", Tab: "tab1", Pane: "pane1", Worktree: h.worktree("A"),
			Left: "START_FAILED"}}
		if got := saved(t, h); !reflect.DeepEqual(got, want) {
			t.Fatalf("saved %+v\nwant %+v", got, want)
		}
		h.herdr.mu.Lock()
		h.herdr.refuseArgs = false
		h.herdr.mu.Unlock()

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if ev := h.sink.text(); !strings.Contains(ev, "  carried over from the last run: A (left running: START_FAILED)") {
			t.Errorf("events:\n%s", ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v; want A", got)
		}
		if got := saved(t, h); len(got) != 0 {
			t.Errorf("run 2 saved %+v; want nothing left", got)
		}
	})
}
