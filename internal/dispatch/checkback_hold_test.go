package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
)

// rebasedHook is git in memory that calls rebased after each rebase.
type rebasedHook struct {
	*fakeGit
	rebased func()
}

func (g rebasedHook) Rebase(ctx context.Context, worktree, onto string) (string, error) {
	defer g.rebased()
	return g.fakeGit.Rebase(ctx, worktree, onto)
}

// orchestra-lx4d.2: a stop added as tickets start, here the main checkout found dirty while A's check
// runs, holds the run at once: A's check failing before Run's next pass isn't handed back to its
// worker, as it would be were the merge to read the run as taking tickets until then.
func TestACheckIsNotHandedBackOnceTheRunHoldsInTheSamePass(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		// The first check waits for the go-ahead on a pipe; each fails.
		pipe := filepath.Join(dir, "go")
		if err := syscall.Mkfifo(pipe, 0o600); err != nil {
			t.Fatal(err)
		}
		h := newTimedHarness(t)
		h.cfg.Check = "test -f '" + filepath.Join(dir, "A.runs") + "' || cat '" + pipe + "' >/dev/null; " +
			countedCheck(dir, "")
		h.cfg.CheckHandBacks = 1
		h.cfg.Concurrency = 2 // a slot left once A starts, for which Run reads the checkout
		h.beads.add("A", "first", 1)
		h.worker("A", landsMeanwhile("a.txt"), commitsFix(dir, "fix.txt", false)) // the fix, were A handed back
		var dirty atomic.Bool
		o := h.loop()
		o.checkout = dirtyCheckout{fakeGit: h.mem, dirty: &dirty}
		// Once A is rebased (the bubble's clock stands still from here until A's check ends)…
		var once sync.Once
		o.merger = rebasedHook{fakeGit: h.mem, rebased: func() {
			once.Do(func() {
				go func() {
					f, err := os.OpenFile(pipe, os.O_WRONLY, 0) // …and its check runs…
					if err != nil {
						t.Error(err)
						return
					}
					dirty.Store(true)
					o.gate.wakeRun() // …Run passes once more, finds the checkout dirty and holds…
					h.waitHeld()
					if err := f.Close(); err != nil { // …and the check fails
						t.Error(err)
					}
				}()
			})
		}}
		o.Run(t.Context())
		if !h.sink.warnedWith("CHECKS_FAILED", "A", "A closed, but '"+h.cfg.Check+"' fails on wt/A rebased onto main; "+
			"not handed back to its worker: the run holds (DIRTY_TREE: ") || h.sink.tagged("FIXING") {
			t.Errorf("events:\n%s", h.sink.text())
		}
		if p := h.herdr.pastedTo(); len(p) > 0 {
			t.Errorf("nothing should be pasted: %v", p)
		}
		if n := checkRuns(t, dir, "A"); n != 1 {
			t.Errorf("A's check ran %d times, want 1", n)
		}
	})
}
