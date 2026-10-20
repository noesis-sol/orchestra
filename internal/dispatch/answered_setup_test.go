package dispatch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// A ticket back from a question whose worktree's setup then fails is back in the queue like any
// other whose setup failed, no longer set aside as a ticket that asked: a merge lets it be taken
// again, and the run's end names it as waiting on its setup (orchestra-3w84).

// A asks Q and its worker goes; B answers Q and merges a changed lockfile, which A's rebase brings
// in, and from then on A's setup fails. C's merge lets A be taken again, its setup fails again, and
// the run ends naming A under SETUP_WAITS, not set aside.
func TestAnAnsweredTicketWhoseSetupFailsIsTakenAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		broken := filepath.Join(t.TempDir(), "broken")
		cmd, log := recordsSetups(t, `test "$(basename "$PWD")" != A || test ! -f `+command.ShellQuote(broken)+
			` || { echo 'npm ERR! offline'; exit 1; }`)
		h.cfg.Setup, h.cfg.SetupFiles = cmd, project.DefaultSetupFiles
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.add("C", "third", 3)
		h.worker("A", asksAndGoes)
		h.worker("B", func(w *fakeWorker) AgentState {
			if err := os.WriteFile(broken, nil, 0o644); err != nil {
				t.Error(err)
			}
			return answersThenFinishes("package-lock.json")(w)
		})
		h.worker("C", finishes("c.txt"))
		o, code := h.run()
		out := filepath.Join(h.worktree("A"), ".orchestra", "run", setupLogName)
		final := "READY_EMPTY after 3 tickets; SETUP_WAITS: A waits on the setup that failed in its worktree " +
			"(the whole output is in " + out + "); fix the setup, then run again"
		if code != ExitOK || o.Final() != final {
			t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), final, h.sink.text())
		}
		if got := setUpIn(t, log); !equal(got, []string{"A", "B", "A", "C", "A"}) {
			t.Errorf("the setup ran in %v, want A's, B's, A's again, C's, then A's once more\n%s", got, h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"B", "C"}) {
			t.Errorf("merged %v, want B, then C", got)
		}
		if aside := o.asideList(); len(aside) != 0 {
			t.Errorf("set aside: %v, want none: A waits on its setup", aside)
		}
		if st := h.statusOf("A"); st != StatusOpen {
			t.Errorf("A is %s, want open", st)
		}
	})
}

// claimedReady is a tracker whose bd ready lists ticket id as in progress, as a worker claimed it,
// and which fails to reopen it.
type claimedReady struct {
	*fakeBeads
	id string
}

var errReopen = errors.New("bd update: database locked")

func (c claimedReady) Ready(ctx context.Context, scope string) ([]Ticket, error) {
	ready, err := c.fakeBeads.Ready(ctx, scope)
	for i := range ready {
		if ready[i].ID == c.id {
			ready[i].Status = StatusInProgress
		}
	}
	return ready, err
}

func (c claimedReady) Reopen(ctx context.Context, id string) error {
	if id == c.id {
		return errReopen
	}
	return c.fakeBeads.Reopen(ctx, id)
}

// A ticket whose setup failed and which bd then failed to reopen isn't in bd ready: the run's end
// doesn't list it under SETUP_WAITS as ready and waiting on its setup, as REOPEN_FAILED said why.
func TestATicketNotReopenedAfterItsSetupFailedIsNotListedAsWaiting(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		cmd, _ := recordsSetups(t, failsInEach("A", "B"))
		h.cfg.Setup = cmd
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		o := h.loop()
		tracker := claimedReady{h.beads, "A"}
		o.tickets, o.notes = tracker, tracker
		code := o.Run(context.Background())
		out := filepath.Join(h.worktree("B"), ".orchestra", "run", setupLogName)
		final := "READY_EMPTY after 0 tickets; SETUP_WAITS: B waits on the setup that failed in its worktree " +
			"(the whole output is in " + out + "); fix the setup, then run again"
		if code != ExitOK || o.Final() != final {
			t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), final, h.sink.text())
		}
		if !h.sink.warnedWith("REOPEN_FAILED", "A", "A stays in_progress, so it won't be back in the queue") {
			t.Errorf("warnings on A %q, want REOPEN_FAILED", warnings(h, "A"))
		}
	})
}
