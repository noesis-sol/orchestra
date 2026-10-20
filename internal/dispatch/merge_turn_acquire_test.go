package dispatch

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
)

// A merge takes its turn in the merge queue with acquire, which returns errInterrupted, and no turn,
// once the run stops: nothing after the stop proceeds without the turn, whatever the merge checks.

// B's merge waits behind A's turn as the run stops: acquire gives up with errInterrupted, the merge
// ends there with nothing rebased, and the queue stays A's until A lets go of it.
func TestAStopEndsAMergeAtAcquire(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Check = "true"
		o := h.loop()
		var mu sync.Mutex
		var rebased []string
		o.merger = rebaseRecording{Merger: o.merger, mu: &mu, rebased: &rebased}
		a, stop := o.merges.acquire(t.Context(), "A")
		if stop != nil {
			t.Fatal(stop)
		}
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		var got *stopReason
		done := make(chan struct{})
		go func() {
			got = o.merge(ctx, worker{id: "B", br: "wt/B", wt: h.worktree("B"), tab: "tab-B"})
			close(done)
		}()
		synctest.Wait()
		if q := &o.merges; len(q.line) != 1 || q.line[0].id != "B" {
			t.Fatalf("B should wait in line behind A: %+v", q.line)
		}
		cancel(InterruptedError("with Ctrl+C"))
		<-done
		if got != errInterrupted {
			t.Errorf("the merge returned %v, want errInterrupted", got)
		}
		mu.Lock()
		if len(rebased) > 0 {
			t.Errorf("rebased %v after the stop, want nothing", rebased)
		}
		mu.Unlock()
		if q := &o.merges; q.holder != "A" || len(q.line) != 0 {
			t.Errorf("the queue should be A's, with none in line: held by %q, %d waiting", q.holder, len(q.line))
		}
		a.release()
		if q := &o.merges; q.holder != "" {
			t.Errorf("A's release should leave the queue free, not held by %q", q.holder)
		}
	})
}

// B's turn comes as the run stops: acquire returns errInterrupted all the same, and passes the queue
// on to C rather than keep it.
func TestATurnThatComesAsTheRunStopsIsPassedOn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		o := New(Config{Base: "main"}, nil, "", Deps{})
		o.sink = &stageSink{}
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

		q := &o.merges
		q.mu.Lock() // B wakes to the stop and its turn both
		stopB()
		a.held = false
		q.passOn() // B's turn, as the run stops
		q.mu.Unlock()
		<-bDone
		<-cDone
		if b != nil || bStop != errInterrupted {
			t.Errorf("B should take no turn after the stop: turn %v, %v", b, bStop)
		}
		if c == nil || cStop != nil {
			t.Fatalf("C should have its turn: %v", cStop)
		}
		if q.holder != "C" {
			t.Errorf("the queue should be C's, not %q's", q.holder)
		}
		c.release()
	})
}

// trainStopSink passes events and statuses on to Sink, and Ctrl+C comes as B is shown checked ahead
// of its turn, rebased onto A's branch.
type trainStopSink struct {
	Sink
	cancel context.CancelCauseFunc
}

func (s trainStopSink) Status(st Status) {
	s.Sink.Status(st)
	if st.Ticket == "B" && strings.HasPrefix(st.Activity, "checked ahead of its turn") {
		s.cancel(InterruptedError("with Ctrl+C"))
	}
}

// Ctrl+C comes as B, waiting behind A, is rebased onto A's branch to be checked ahead of its turn. B
// takes no turn, and its branch is put back as its worker left it, without A's commits: A didn't merge.
func TestAStopBeforeItsTurnPutsBackABranchRebasedAhead(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, g, o := trainLoop(t, 1, `id=$(basename "$PWD"); if [ "$id" = A ]; then sleep 30; fi`)
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		o.SetSink(trainStopSink{Sink: o.sink, cancel: cancel})
		if code := o.Run(ctx); code != ExitInterrupted {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		if got := g.rebasedOnto(h.worktree("B")); len(got) != 1 {
			t.Fatalf("B was rebased onto %v, want once, onto A's branch ahead of its turn\n%s", got, ev)
		}
		if got := closedIDs(h); len(got) != 0 {
			t.Errorf("merged %v after the stop, want none", got)
		}
		if b := h.mem.log("wt/B"); strings.Contains(b, "A: add a.txt") || !strings.Contains(b, "B: add b.txt") {
			t.Errorf("wt/B should be as B's worker left it, without A's commit:\n%s", b)
		}
		want := "wt/B was rebased onto wt/A to be checked ahead of its turn, but the run stopped before its turn: " +
			"put back as its worker left it\n"
		if !strings.Contains(ev, want) {
			t.Errorf("events lack %q:\n%s", want, ev)
		}
	})
}
