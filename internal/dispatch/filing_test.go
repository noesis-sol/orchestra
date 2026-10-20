package dispatch

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// filingHarness is a timed run of the spec tree R, its environment ticket R.1 (solo, priority 0)
// filed and ready, and A, under R too, ready alongside it but not to start before the filing is over,
// with Done its filing's: until it is closed only R.1 may start.
func filingHarness(t *testing.T) (h *harness, done chan struct{}) {
	t.Helper()
	h = scoped(newTimedHarness(t), "R")
	h.cfg.Concurrency = 2
	h.beads.epic("R", "")
	h.beads.sub("R.1", "R", "set up the project", 0, SoloLabel)
	h.beads.sub("A", "R", "unrelated to the setup", 1)
	done = make(chan struct{})
	h.cfg.Filing = &Filing{First: "R.1", Done: done}
	return h, done
}

// While the scope is being filed only its first ticket starts, though others under it are ready;
// the run waits, once it is done, for the filing to be over rather than end, and then takes the
// rest, filed meanwhile.
func TestFilingRunStartsItsFirstTicketAlone(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, done := filingHarness(t)
		h.worker("R.1", finishes("setup.txt"))
		h.worker("A", finishes("a.txt"))
		h.worker("B", finishes("b.txt"))
		go func() {
			time.Sleep(20 * time.Minute) // the breakdown goes on long after R.1 has merged
			h.beads.sub("B", "R", "filed last", 2)
			if got := h.sink.dispatched(); !equal(got, []string{"R.1"}) {
				t.Errorf("dispatched before the filing was over: %v, want R.1 alone", got)
			}
			close(done)
		}()
		o, code := h.run()
		if code != ExitOK || !strings.HasPrefix(o.Final(), "READY_EMPTY after 3 tickets") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.dispatched(); !equal(got, []string{"R.1", "A", "B"}) {
			t.Errorf("dispatched %v, want R.1, then A and B", got)
		}
		ev := h.sink.text()
		if !before(ev, "  waiting for the rest of R to be filed", "  the rest of R is filed: its tickets join the run") {
			t.Errorf("events:\n%s", ev)
		}
	})
}

// The filing over while the first ticket's worker still works, the rest starts once that solo
// ticket is done, without waiting for a poll.
func TestFilingOverWhileTheFirstTicketRuns(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, done := filingHarness(t)
		h.worker("R.1", func(w *fakeWorker) AgentState {
			time.Sleep(time.Minute)
			close(done)
			time.Sleep(time.Minute)
			return finishes("setup.txt")(w)
		})
		h.worker("A", finishes("a.txt"))
		o, code := h.run()
		if code != ExitOK || !strings.HasPrefix(o.Final(), "READY_EMPTY after 2 tickets") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.dispatched(); !equal(got, []string{"R.1", "A"}) {
			t.Errorf("dispatched %v", got)
		}
		if ev := h.sink.text(); strings.Contains(ev, "waiting for the rest of R") {
			t.Errorf("the run waited for a filing already over:\n%s", ev)
		}
	})
}

// A filing that fails cancels the run with its cause, leaving Done open: the first ticket's worker
// is left with its tab and worktree, as a stopped run leaves its workers, and nothing else starts.
func TestFilingThatFailsStopsTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, _ := filingHarness(t)
		started, release := make(chan struct{}), make(chan struct{})
		h.worker("R.1", func(w *fakeWorker) AgentState { w.claim(); close(started); <-release; return "idle" })
		defer close(release)
		o := h.loop()
		o.ReportInterrupt = true
		ctx, cancel := context.WithCancelCause(t.Context())
		codes := make(chan int, 1)
		go func() { codes <- o.Run(ctx) }()
		<-started
		cancel(InterruptedError("because the spec's breakdown failed"))
		code := <-codes
		want := "INTERRUPTED: stopped because the spec's breakdown failed while R.1 (tab " + o.Running()[0].Tab +
			") were running; their tabs and worktrees are left open"
		if code != ExitInterrupted || o.Final() != want {
			t.Errorf("exit %d, final %q, want %q", code, o.Final(), want)
		}
		if got := h.sink.dispatched(); !equal(got, []string{"R.1"}) {
			t.Errorf("dispatched %v", got)
		}
		if len(h.herdr.tabsClosed()) != 0 || !exists(h.worktree("R.1")) {
			t.Error("the worker's tab and worktree should be left")
		}
	})
}
