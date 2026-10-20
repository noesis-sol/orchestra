package dispatch

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A worker that finds its ticket needs another ticket's code, not merged yet, links that ticket as
// a blocker and stops with nothing done. Its ticket started too early: it goes back in the queue,
// open, rather than be set aside, and runs again once the blocker merges. With work of its own, or
// a blocker merged already, it is set aside for review as before.

// needs is a worker that finds its ticket needs blocker's code: it links blocker as blocking its
// ticket and stops, leaving the ticket status, without a commit.
func needs(blocker string, status TicketStatus) behaviour {
	return func(w *fakeWorker) AgentState {
		w.beads.set(w.id, status)
		w.beads.link(w.id, blocker, "blocks")
		return "idle"
	}
}

// slowly claims the ticket, works on it for d, then commits file and closes it.
func slowly(d time.Duration, file string) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		time.Sleep(d)
		w.commit(file)
		w.close()
		return "idle"
	}
}

// B starts beside A, and its worker finds B needs A's code: B goes back in the queue, open, its
// worktree, branch and tab removed, without triage or a count toward the hold for the environment,
// and starts again from a base with A's code once A merges. A worker that claimed B first, and
// leaves it in progress, does the same.
func TestTicketStartedBeforeItsBlockerMergedWaitsForIt(t *testing.T) {
	t.Parallel()
	for _, left := range []TicketStatus{StatusOpen, StatusInProgress} {
		t.Run(string(left), func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				h.cfg.Concurrency = 2
				// One worker failing at once would hold the run; B's must not count as one.
				h.cfg.EnvHoldCount, h.cfg.EnvHoldWindow = 1, time.Hour
				h.beads.add("A", "ticket A", 1)
				h.beads.add("B", "ticket B", 2)
				h.worker("A", slowly(2*idleGrace, "a.txt")) // still running when B settles
				var firstTab string
				h.worker("B", func(w *fakeWorker) AgentState {
					firstTab = tabOf(h, "B")
					return needs("A", left)(w)
				}, cutWith("A: add a.txt", "b.txt"))
				o := h.loop()
				o.StartTriage() // without claude: a deferral queued fails its triage, out loud
				code := o.Run(t.Context())
				o.FinishTriage(t.Context())

				if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if got := closedIDs(h); !equal(got, []string{"A", "B"}) {
					t.Errorf("merged %v, want A, then B\n%s", got, h.sink.text())
				}
				if got := h.dispatched(); !equal(got, []string{"A", "B", "B"}) {
					t.Errorf("dispatched %v, want A, B, then B again", got)
				}
				waits := h.sink.of(EvWaits)
				if len(waits) != 1 || !strings.HasPrefix(waits[0], "B   WAITS_FOR_MERGE: B waits for A to merge; ") ||
					!strings.HasSuffix(waits[0], "worktree, branch and tab removed") {
					t.Errorf("waits events %q, want one for B, its worktree, branch and tab removed", waits)
				}
				if !slices.Contains(h.herdr.tabsClosed(), firstTab) {
					t.Errorf("B's first tab %s left open; closed %v", firstTab, h.herdr.tabsClosed())
				}
				if n := h.beads.notesOf("B"); !strings.Contains(n,
					"Orchestra: started before A merged; back in the queue until it does.") || strings.Contains(n, "deferred") {
					t.Errorf("B's notes:\n%s", n)
				}
				ev := h.sink.text()
				for _, not := range []string{"deferred", "TRIAGE_FAILED", "ENVIRONMENT", "PAUSED"} {
					if strings.Contains(ev, not) {
						t.Errorf("events say %s:\n%s", not, ev)
					}
				}
				if aside := o.asideList(); len(aside) != 0 {
					t.Errorf("set aside: %v", aside)
				}
			})
		})
	}
}

// B's worker commits work of its own before it finds B needs A: that is work to review, so B is
// deferred as before, and doesn't run again.
func TestTicketWithCommitsBlockedByARunningTicketIsDeferred(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "ticket A", 1)
		h.beads.add("B", "ticket B", 2)
		h.worker("A", slowly(2*idleGrace, "a.txt"))
		h.worker("B", func(w *fakeWorker) AgentState {
			w.commit("b.txt")
			return needs("A", StatusOpen)(w)
		})
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.of(EvDeferred); len(got) != 1 || !h.sink.detailed(EvDeferred, "B", "still open, noted for review") {
			t.Errorf("deferred %q, want B, noted for review", got)
		}
		if got := h.sink.of(EvWaits); len(got) != 0 {
			t.Errorf("waits %q, want none", got)
		}
		if st := h.statusOf("B"); st != StatusDeferred {
			t.Errorf("B is %s, want deferred", st)
		}
	})
}

// A has merged by the time B starts, and B's worker links it as a blocker anyway: waiting would
// change nothing, so B is deferred as before.
func TestTicketBlockedByAMergedTicketIsDeferred(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "ticket A", 1)
		h.beads.add("B", "ticket B", 2)
		h.worker("A", finishes("a.txt"))
		h.worker("B", needs("A", StatusOpen))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v, want A", got)
		}
		if got := h.sink.of(EvDeferred); len(got) != 1 || !h.sink.detailed(EvDeferred, "B", "still open, noted for review") {
			t.Errorf("deferred %q, want B, noted for review", got)
		}
		if got := h.sink.of(EvWaits); len(got) != 0 {
			t.Errorf("waits %q, want none", got)
		}
	})
}

// B's worker links a blocker closed but left unmerged by an earlier run: B waits for it, as the
// holds do, rather than be deferred.
func TestTicketBlockedByAnUnmergedTicketWaitsForIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("U", "conflicted", 1, UnmergedLabel)
		h.beads.set("U", "closed")
		h.beads.add("B", "ticket B", 2)
		h.worker("B", needs("U", StatusOpen)) // once: a second dispatch fails the test
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.of(EvWaits); len(got) != 1 || !strings.HasPrefix(got[0], "B   WAITS_FOR_MERGE: B waits for U to merge") {
			t.Errorf("waits %q, want B waiting for U", got)
		}
		if ev := h.sink.text(); !strings.Contains(ev, "B waits: U closed but not merged ("+earlierRun+")") {
			t.Errorf("B should be held for U:\n%s", ev)
		}
		if st := h.statusOf("B"); st != StatusOpen {
			t.Errorf("B is %s, want open", st)
		}
	})
}
