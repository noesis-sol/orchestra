package dispatch

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// A stop that comes while a finished ticket waits for its turn in the merge queue, or as its rebase
// stops on conflicts, ends its merge there: its branch isn't rebased after the stop, nor handed back
// to a worker that is never prompted, and the next run carries it over as it does any ticket left
// finished and unmerged.

// queuedStopSink passes events and statuses on to Sink, and Ctrl+C comes as ticket on is shown
// waiting for its turn to merge. It keeps each ticket shown as handed back to its worker.
type queuedStopSink struct {
	Sink
	on     string
	cancel context.CancelCauseFunc
	mu     *sync.Mutex
	handed *[]string
}

func (s queuedStopSink) Status(st Status) {
	s.Sink.Status(st)
	s.mu.Lock()
	if st.Resolving || st.Fixing {
		*s.handed = append(*s.handed, st.Ticket)
	}
	s.mu.Unlock()
	if st.Ticket == s.on && st.Merge == MergeQueued {
		s.cancel(InterruptedError("with Ctrl+C"))
	}
}

// rebaseRecording is git, keeping the worktrees it is asked to rebase.
type rebaseRecording struct {
	Merger
	mu      *sync.Mutex
	rebased *[]string
}

func (m rebaseRecording) Rebase(ctx context.Context, worktree, onto string) (string, error) {
	m.mu.Lock()
	*m.rebased = append(*m.rebased, worktree)
	m.mu.Unlock()
	return m.Merger.Rebase(ctx, worktree, onto)
}

// unconflict lets the rebases of branch go through again (see conflict).
func (g *fakeGit) unconflict(branch string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.conflicts, branch)
}

// orchestra-thcc: B waits behind A's merge, which holds the queue as it closes A's tab, when Ctrl+C
// comes. Once A lets the queue go, B takes no turn: its branch, which would conflict with A's on
// main, isn't rebased, and nothing hands it back to its worker. The run saves it for the next, which
// merges it.
func TestAStopEndsAMergeWaitingForItsTurn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.cfg.Check = "true"
		h.cfg.ResolveConflicts = true
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", finishes("a.txt"))
		h.worker("B", busyFor(time.Minute, "b.txt")) // finishes as A's merge closes A's tab
		h.mem.conflict("wt/B", "shared.txt")
		o := h.loop()
		o.ReportInterrupt = true
		o.tabs = slowTabClose{fakeHerdr: h.herdr, ticket: "A", takes: time.Hour}
		var mu sync.Mutex
		var rebased, handed []string
		o.merger = rebaseRecording{Merger: o.merger, mu: &mu, rebased: &rebased}
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		o.SetSink(queuedStopSink{Sink: h.sink, on: "B", cancel: cancel, mu: &mu, handed: &handed})
		if code := o.Run(ctx); code != ExitInterrupted {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		if ctx.Err() == nil {
			t.Fatalf("Ctrl+C never came:\n%s", ev)
		}
		mu.Lock()
		if slices.Contains(rebased, h.worktree("B")) {
			t.Errorf("B's branch was rebased after the stop:\n%s", ev)
		}
		if len(handed) > 0 {
			t.Errorf("shown handed back to their workers: %v\n%s", handed, ev)
		}
		mu.Unlock()
		if h.mem.inRebase(h.worktree("B")) {
			t.Error("B's worktree is left mid-rebase")
		}
		if h.sink.tagged("RESOLVING") || strings.Contains(o.Final(), "resolv") {
			t.Errorf("nothing was handed back to resolve: final %q\n%s", o.Final(), ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v, want A", got)
		}
		if got := saved(t, h); len(got) != 1 || got[0].Ticket != "B" || got[0].Worktree != h.worktree("B") ||
			got[0].Left != "INTERRUPTED" {
			t.Fatalf("saved %+v, want B, left INTERRUPTED\n%s", got, ev)
		}

		h.mem.unconflict("wt/B")
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev = h.sink.text()
		if !strings.Contains(ev, "  carried over from the last run: B (left running: INTERRUPTED)") {
			t.Errorf("run 2 should carry B over:\n%s", ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A", "B"}) {
			t.Errorf("merged %v, want A, then B", got)
		}
	})
}

// orchestra-thcc: Ctrl+C comes as B's rebase stops on conflicts. B isn't handed back to its worker,
// which could no longer be prompted: its rebase is aborted, its branch left as its worker left it,
// and the run saves it for the next.
func TestAStopAsARebaseConflictsAbortsIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Check = "true"
		h.cfg.ResolveConflicts = true
		h.beads.add("B", "second", 1)
		h.worker("B", landsMeanwhile("b.txt"))
		h.mem.conflict("wt/B", "shared.txt")
		o := h.loop()
		o.ReportInterrupt = true
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		o.merger = rebaseInterrupting{Merger: o.merger, wt: h.worktree("B"), n: 1, cancel: cancel,
			mu: &sync.Mutex{}, seen: new(int)}
		var mu sync.Mutex
		var handed []string
		o.SetSink(queuedStopSink{Sink: h.sink, cancel: cancel, mu: &mu, handed: &handed})
		if code := o.Run(ctx); code != ExitInterrupted {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		if len(handed) > 0 || h.sink.tagged("RESOLVING") || strings.Contains(o.Final(), "resolv") {
			t.Errorf("nothing was handed back to resolve (shown handed back: %v): final %q\n%s", handed, o.Final(), ev)
		}
		if h.mem.inRebase(h.worktree("B")) {
			t.Error("B's rebase should be aborted")
		}
		if got := saved(t, h); len(got) != 1 || got[0].Ticket != "B" || got[0].Left != "INTERRUPTED" {
			t.Errorf("saved %+v, want B, left INTERRUPTED\n%s", got, ev)
		}
	})
}

// A ticket waiting in the merge queue as the run stops leaves the line without taking a turn: those
// behind it move up, and acquire returns errInterrupted with no turn to release.
func TestMergeQueueLetsATicketGiveUpItsTurnAsTheRunStops(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		sink := &stageSink{}
		o := New(Config{Base: "main"}, nil, "", Deps{})
		o.sink = sink
		for _, id := range []string{"A", "B", "C"} {
			o.setActive(Status{Ticket: id, Title: "Ticket " + id, Tab: "tab-" + id, Started: time.Now()})
		}
		a, stop := o.merges.acquire(t.Context(), "A")
		if stop != nil {
			t.Fatal(stop)
		}
		ctxB, stopB := context.WithCancel(t.Context())
		defer stopB()
		var b, c *mergeTurn
		var bStop, cStop *stopReason
		bDone, cDone := make(chan struct{}), make(chan struct{})
		go func() { b, bStop = o.merges.acquire(ctxB, "B"); close(bDone) }()
		synctest.Wait()
		go func() { c, cStop = o.merges.acquire(t.Context(), "C"); close(cDone) }()
		synctest.Wait()
		sink.shown()

		stopB()
		<-bDone
		if b != nil || bStop != errInterrupted {
			t.Fatalf("B should give up waiting as the run stops: turn %v, %v", b, bStop)
		}
		synctest.Wait()
		select {
		case <-cDone:
			t.Fatal("C should wait on behind A")
		default:
		}
		if got, want := sink.shown(), []string{"C to merge 1"}; !slices.Equal(got, want) {
			t.Errorf("shown %q, want %q: C moves up", got, want)
		}
		b.release() // a nil turn releases nothing
		if q := &o.merges; q.holder != "A" {
			t.Errorf("the queue should stay A's: held by %q", q.holder)
		}
		a.release()
		<-cDone
		if cStop != nil {
			t.Fatalf("C: %v", cStop)
		}
		c.release()
		c.release() // a turn released twice passes nothing on the second time
		if q := &o.merges; q.holder != "" || len(q.line) != 0 {
			t.Errorf("the queue should be empty: held by %q, %d waiting", q.holder, len(q.line))
		}
	})
}
