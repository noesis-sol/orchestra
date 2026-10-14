package dispatch

import (
	"context"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
)

// rebaseInterrupting is git, but Ctrl+C comes as the worktree wt is rebased for the nth time.
type rebaseInterrupting struct {
	Merger
	wt     string
	n      int
	cancel context.CancelCauseFunc
	mu     *sync.Mutex
	seen   *int
}

func (m rebaseInterrupting) Rebase(ctx context.Context, worktree, onto string) (string, error) {
	m.mu.Lock()
	if worktree == m.wt {
		if *m.seen++; *m.seen == m.n {
			m.cancel(InterruptedError("with Ctrl+C"))
		}
	}
	m.mu.Unlock()
	return m.Merger.Rebase(ctx, worktree, onto)
}

// orchestra-74b2.33: Ctrl+C as a ticket set aside for its failed check is checked once more cuts
// only the check short. No worker of it was running, so the INTERRUPTED line doesn't name it as
// one left running; it stays set aside and labelled, and isn't saved for the next run.
func TestCtrlCDuringARecheckLeavesTheTicketSetAside(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = countedCheck(dir, "") // never fixed
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", landsMeanwhile("a.txt"))
		h.worker("B", finishes("b.txt"))
		o := h.loop()
		o.ReportInterrupt = true
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		// A is rebased once as it merges, and once more as it is checked again after B merged.
		o.merger = rebaseInterrupting{Merger: o.merger, wt: h.worktree("A"), n: 2, cancel: cancel,
			mu: &sync.Mutex{}, seen: new(int)}
		code := o.Run(ctx)
		want := "INTERRUPTED: stopped with Ctrl+C; a running worker keeps its tab and worktree"
		if code != ExitInterrupted || o.Final() != want {
			t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), want, h.sink.text())
		}
		ev := h.sink.text()
		if !strings.Contains(ev, "RECHECK: A") || !strings.Contains(ev, "  A's check once more was cut short; it stays set aside\n") {
			t.Errorf("events should say A's check once more was cut short:\n%s", ev)
		}
		if strings.Contains(ev, "A is left running") {
			t.Errorf("A has no worker running:\n%s", ev)
		}
		if aside := o.asideList(); !equal(aside, []string{"A"}) {
			t.Errorf("set aside %v, want A", aside)
		}
		if !unmergedLabel(t, h, "A") {
			t.Error("A should stay labelled unmerged")
		}
		if got := saved(t, h); len(got) != 0 {
			t.Errorf("saved %+v; want nothing: A stays set aside", got)
		}
		if got := closedIDs(h); !equal(got, []string{"B"}) {
			t.Errorf("merged %v, want B", got)
		}
	})
}
