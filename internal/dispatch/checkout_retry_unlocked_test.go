package dispatch

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/synctest"
)

// signallingCheckout is a main checkout whose git status fails once, saying so on failed.
type signallingCheckout struct {
	cleanCheckout
	failed chan struct{}
}

func (s signallingCheckout) DirtyTree(ctx context.Context, dir string) (string, error) {
	select {
	case <-s.failed:
		return "", nil
	default:
		close(s.failed)
		return "", errors.New("git -C repo status --porcelain: signal: killed")
	}
}

// orchestra-74b2.33: while next waits to read the main checkout again after git failed, it doesn't
// hold repoMu, so the merges and the worktrees being made go on meanwhile.
func TestNextLetsGoOfTheRepositoryBetweenTries(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		log, err := OpenLog(filepath.Join(t.TempDir(), "orchestra.log"), false, "t")
		if err != nil {
			t.Fatal(err)
		}
		failed := make(chan struct{})
		o := New(Config{Repo: "repo", Base: "main"}, log, "",
			Deps{Tickets: readyTickets{{ID: "A"}}, Checkout: signallingCheckout{failed: failed}})
		picked := make(chan *Ticket, 1)
		go func() {
			tk, _, s := o.next(t.Context(), nil)
			if s != nil {
				t.Errorf("stopped: %v", s)
			}
			picked <- tk
		}()
		<-failed
		synctest.Wait() // next waits to try again
		if !o.repoMu.TryLock() {
			t.Fatal("next holds repoMu while it waits to read the checkout again")
		}
		o.repoMu.Unlock()
		if tk := <-picked; tk == nil || tk.ID != "A" {
			t.Errorf("picked %+v, want A", tk)
		}
	})
}
