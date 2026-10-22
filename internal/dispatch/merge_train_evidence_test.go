package dispatch

import (
	"context"
	"testing"
	"testing/synctest"
)

// orchestra-xj6z.9: a ticket set aside for its failed check, checked once more, and failing its check
// ahead of its turn in the merge train, lost what its first check said: the train's check overwrote it,
// then dropped it, and the run report said nothing of the ticket's check if its turn never checked it.
// A check ahead of its turn, on a branch not yet merged, says nothing of the ticket: the evidence stays.
func TestAFailedTrainCheckKeepsTheEvidenceOfATicketSetAside(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Check = "echo '--- FAIL: TestAhead (0.01s)'; exit 1"
		ctx := context.Background()
		wt := h.worktree("B")
		if _, err := h.mem.NewWorktree(ctx, h.repo, wt, "wt/B", "main"); err != nil {
			t.Fatal(err)
		}
		h.mem.commit("wt/B", "B: add b.txt", "b.txt")
		ahead, err := h.mem.Head(ctx, h.repo, "main")
		if err != nil {
			t.Fatal(err)
		}
		o := h.loop()
		earlier := checkFail{br: "wt/B", said: []string{"--- FAIL: TestEarlier (0.01s)"}}
		o.book.checkFailed("B", earlier)
		w := worker{workerRef: workerRef{wt: wt, tab: "tab1"}, id: "B", br: "wt/B"}
		tr := &trainRun{id: "B", behind: "wt/A", onto: ahead, cancel: func() {}, done: make(chan struct{})}
		o.runTrain(ctx, w, tr)
		if !tr.failed || tr.cut || tr.why == "" {
			t.Fatalf("train failed %v, cut %v, why %q; want its check failed\n%s", tr.failed, tr.cut, tr.why,
				h.sink.text())
		}
		r, _ := o.book.get("B")
		if r.failedCheck == nil || !equal(r.failedCheck.said, earlier.said) {
			t.Errorf("B's failed check %+v, want the earlier one's evidence kept: %v", r.failedCheck, earlier.said)
		}
	})
}
