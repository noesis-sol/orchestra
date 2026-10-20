package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A's worker files F, and links it discovered-from A only in a second command (bd create, then bd
// dep add), after the loop has read F's links without it: bd ready shows nothing of the new link.
// When B's slot frees, F is picked, but its links are read again first, and F waits for A to merge.
func TestFollowUpLinkedAfterItsLinksWereReadWaitsForItsSource(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "ticket A", 1)
		h.beads.add("B", "ticket B", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.beads.add("F", "follow-up of A", 2)
			time.Sleep(2 * readyPoll) // no slot free: the loop reads F's links, which have no source yet
			w.beads.link("F", "A", "discovered-from")
			time.Sleep(6 * readyPoll) // B finishes meanwhile, freeing a slot
			return finishes("a.txt")(w)
		})
		h.worker("B", busyFor(4*readyPoll, "b.txt"))
		h.worker("F", cutWith("A: add a.txt", "f.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"B", "A", "F"}) {
			t.Errorf("merged %v, want B, A, then F\n%s", got, h.sink.text())
		}
		if ev := h.sink.text(); !strings.Contains(ev, "F waits: filed by A's worker; waiting for it to merge") {
			t.Errorf("events lack F waiting for A:\n%s", ev)
		}
	})
}

// k-a, blocking k-b, merges, and k-d, closed but not merged yet, takes its place as k-b's blocker:
// bd ready still counts one. With a slot free, k-b is picked, its links are read again, and it
// waits for k-d.
func TestSwappedBlockerHoldsTheTicketPicked(t *testing.T) {
	t.Parallel()
	f := newMergeFixture(t, "true")
	f.orch.cfg.Concurrency = 2
	one := 1
	tk := aBlocksB()
	tk.ready[0].DependencyCount = &one
	f.orch.tickets = tk
	if got := f.next(t, map[string]bool{"k-a": true}); got != "" {
		t.Fatalf("next = %q, want k-b held while k-a has not merged", got)
	}
	b := tk.shown["k-b"]
	b.Dependencies = []Ticket{{ID: "k-d", Status: "closed", DependencyType: "blocks"}}
	tk.shown["k-b"] = b
	if got := f.next(t, map[string]bool{"k-d": true}); got != "" {
		t.Errorf("next = %q, want k-b held while k-d has not merged", got)
	}
	if ev := f.sink.text(); !strings.Contains(ev, "k-b waits: waiting for k-d to merge") {
		t.Errorf("events:\n%s", ev)
	}
}
