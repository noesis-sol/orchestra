package dispatch

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/organ"
)

// Run reads the asked tickets on its own goroutine, both as it follows them and as it ends, and an
// asked ticket its worker deferred in its tab goes to triage from there. After Ctrl+C triage takes
// nothing, and Run never waits on triage, which may be waiting on Run.

// Ctrl+C with an asked ticket its worker deferred in its tab: the ticket is set aside as deferred
// before the INTERRUPTED line, but no evidence is gathered for triage and nothing is queued for it.
func TestInterruptTriagesNoAskedTicketDeferredInItsTab(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		told, deferred, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
		defer close(release)
		h.worker("A", asksThenCarriesOn(hooks, told, func(w *fakeWorker) AgentState {
			w.deferIt()
			close(deferred)
			return "idle"
		}))
		h.worker("B", func(w *fakeWorker) AgentState {
			w.claim()
			close(told) // the maintainer, in A's tab, has it deferred; Q stays open
			<-release
			return "idle"
		})
		o := h.loop()
		o.ReportInterrupt = true
		reads := countEvidence(o)
		o.StartTriage() // without claude: a deferral queued fails its triage, out loud
		ctx, cancel := context.WithCancelCause(t.Context())
		codes := make(chan int, 1)
		go func() { codes <- o.Run(ctx) }()
		<-deferred
		cancel(InterruptedError("with Ctrl+C"))
		code := <-codes
		o.FinishTriage(t.Context())

		if code != ExitInterrupted || !strings.HasPrefix(o.Final(), "INTERRUPTED: stopped with Ctrl+C while B") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if ev := h.sink.text(); !before(ev, "A deferred by worker after its question Q; worktree ", "INTERRUPTED: ") {
			t.Errorf("A should be set aside as deferred before the INTERRUPTED line:\n%s", ev)
		}
		if a := h.statusOf("A"); a != "deferred" || o.isAsked("A") {
			t.Errorf("A is %s, asked %v; want deferred and no longer asked", a, o.isAsked("A"))
		}
		for _, what := range []string{"bd show", "git status", "git log", "git diff", "screen"} {
			if got := reads.of(what); got != 0 {
				t.Errorf("%s: %d reads, want none after Ctrl+C", what, got)
			}
		}
		if got := h.sink.text(); strings.Contains(got, "TRIAGE_FAILED") {
			t.Errorf("A was queued for triage:\n%s", got)
		}
	})
}

// fillingTickets is the tracker, but as Run gathers the evidence of ticket id for triage, triage
// is first handed a deferral of its own; once triage waits to hand Run its verdict, its queue is
// filled.
type fillingTickets struct {
	Tickets
	o    *Loop
	id   string
	once sync.Once
}

func (f *fillingTickets) Describe(ctx context.Context, id string) string {
	if id == f.id {
		f.once.Do(func() {
			f.o.queueTriage(ctx, organ.Deferral{ID: "X"})
			synctest.Wait() // triage has X's verdict for Run
			for len(f.o.triageQ) < cap(f.o.triageQ) {
				f.o.queueTriage(ctx, organ.Deferral{ID: "queued"}) // there is room: it doesn't wait
			}
		})
	}
	return f.Tickets.Describe(ctx, id)
}

// As a held run ends, an asked ticket its worker deferred in its tab goes to triage, whose queue is
// full while triage waits to hand Run a verdict: Run drops the deferral, says so, and returns,
// rather than wait for room that only it could make.
func TestRunEndsWithTriageFullAndWaitingOnIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.Concurrency = 3
		h.cfg.EnvHoldCount = 2 // triage hands Run its verdicts
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.add("C", "third", 3)
		deferred := make(chan struct{})
		// Once the run holds, follow no longer reads A: only leaveAsked does, as the run ends.
		h.worker("A", asksThenCarriesOn(hooks, h.sink.held, func(w *fakeWorker) AgentState {
			w.deferIt()
			close(deferred)
			return "idle"
		}))
		h.worker("B", func(w *fakeWorker) AgentState { w.claim(); return "idle" }) // pauses the run
		h.worker("C", func(w *fakeWorker) AgentState {
			<-deferred
			return finishes("c.txt")(w)
		})
		o := h.loop()
		o.organ = organ.Client{Bin: fakeTriage(t)}
		organCtx, skipOrgans := context.WithCancel(t.Context())
		defer skipOrgans()
		o.organCtx = organCtx
		o.tickets = &fillingTickets{Tickets: o.tickets, o: o, id: "A"}
		o.StartTriage()
		codes := make(chan int, 1)
		go func() { codes <- o.Run(t.Context()) }()
		var code int
		select {
		case code = <-codes:
		case <-time.After(24 * time.Hour):
			t.Fatalf("Run did not return:\n%s", h.sink.text())
		}
		skipOrgans() // the deferrals queued to fill the queue are dropped
		o.FinishTriage(t.Context())

		if code != ExitStuck || !strings.HasPrefix(o.Final(), "PAUSED: B still in_progress") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := "A   A is not triaged: 64 deferrals wait for triage already"
		if got := h.sink.of(EvInfo); !strings.Contains(strings.Join(got, "\n"), want) {
			t.Errorf("want %q:\n%s", want, h.sink.text())
		}
		if got := h.sink.of(EvDeferred); len(got) != 1 || !strings.HasPrefix(got[0], "A   A deferred by worker after its question Q") {
			t.Errorf("deferred:\n%s", strings.Join(got, "\n"))
		}
		if a := h.statusOf("A"); a != "deferred" {
			t.Errorf("A is %s", a)
		}
		// One queued may be triaged before the organs are skipped, after X.
		if got := h.sink.of(EvTriage); len(got) == 0 || !strings.HasPrefix(got[0], "X   triage X: environment") {
			t.Errorf("X should be triaged first:\n%s", strings.Join(got, "\n"))
		}
	})
}
