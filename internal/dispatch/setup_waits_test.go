package dispatch

import (
	"context"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

// A ticket whose worktree's setup failed waits in bd ready, kept out of the run, until a ticket
// merges: a run that ends with nothing else to merge names it, with where its output is, rather than
// leave only the SETUP_FAILED warning to say why it never ran (orchestra-6zw6).

// failsInEach is a command for recordsSetups to run that fails, printing npm's error, in the
// worktrees of tickets ids, and works anywhere else.
func failsInEach(ids ...string) string {
	cases := ""
	for i, id := range ids {
		if i > 0 {
			cases += "|"
		}
		cases += id
	}
	return `case "$(basename "$PWD")" in ` + cases + `) echo 'npm ERR! offline'; exit 1;; esac`
}

// The run's end line names the tickets still waiting on a failed setup, each with its output, and
// the queue counts them as held back by it, apart from the other holds.
func TestTheRunsEndNamesTheTicketsWaitingOnAFailedSetup(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		cmd, log := recordsSetups(t, failsInEach("A", "B"))
		h.cfg.Setup = cmd
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.add("C", "third", 3)
		h.worker("C", finishes("c.txt"))
		o := h.loop()
		o.log.Begin(h.repo, RunStart{Started: time.Now(), Version: "v1", Repo: h.repo, Branch: "main", Concurrency: 1})
		code := o.Run(context.Background())
		o.log.End(code)
		if err := o.log.Close(); err != nil {
			t.Fatal(err)
		}
		outA := filepath.Join(h.worktree("A"), ".orchestra", "run", setupLogName)
		outB := filepath.Join(h.worktree("B"), ".orchestra", "run", setupLogName)
		final := "READY_EMPTY after 1 tickets; SETUP_WAITS: A, B wait on the setups that failed in their worktrees " +
			"(the whole output is in " + outA + ", " + outB + "); fix the setup, then run again"
		if code != ExitOK || o.Final() != final {
			t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), final, h.sink.text())
		}
		// C's merge let A and B be taken again, and their setups failed again.
		if got := setUpIn(t, log); !equal(got, []string{"A", "B", "C", "A", "B"}) {
			t.Errorf("the setup ran in %v, want A's, B's and C's worktrees, then A's and B's again", got)
		}
		for _, ev := range eventsOf(h, EvDispatch) {
			if ev.Ticket == "C" && (ev.Held != HeldBack{Setup: 2} || ev.Queued != 0) {
				t.Errorf("C dispatched with %d queued and %+v held back, want A and B held for their setup",
					ev.Queued, ev.Held)
			}
		}
		queues := eventsOf(h, EvQueue)
		if len(queues) == 0 || queues[len(queues)-1].Held != (HeldBack{Setup: 2}) {
			t.Errorf("queue reports %+v, want the last to hold A and B back for their setup", queues)
		}
		var last map[string]any
		for _, r := range records(t, h.repo) {
			if r["kind"] == "queue" {
				last = r
			}
		}
		if by, _ := last["held_by"].(map[string]any); last["held"] != 2.0 || len(by) != 1 || by["setup"] != 2.0 {
			t.Errorf("the last queue record %v, want held 2, held_by setup 2", last)
		}
	})
}

// A scoped run says why its ticket waiting on a failed setup isn't done, rather than "not started".
func TestAScopedRunSaysItsTicketWaitsOnAFailedSetup(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		cmd, _ := recordsSetups(t, failsInEach("A"))
		h.cfg.Setup = cmd
		h.cfg.Ticket = "A"
		h.beads.add("A", "first", 1)
		o, code := h.run()
		out := filepath.Join(h.worktree("A"), ".orchestra", "run", setupLogName)
		final := "READY_EMPTY after 0 tickets; SETUP_WAITS: A waits on the setup that failed in its worktree " +
			"(the whole output is in " + out + "); fix the setup, then run again; SCOPE_OPEN: A: its 0 subtickets " +
			"are merged, but A itself is not done (its setup failed; the whole output is in " + out + ")"
		if code != ExitOK || o.Final() != final {
			t.Fatalf("exit %d, final %q, want %q\n%s", code, o.Final(), final, h.sink.text())
		}
	})
}
