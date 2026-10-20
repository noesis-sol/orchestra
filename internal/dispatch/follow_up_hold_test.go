package dispatch

import (
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A follow-up a worker files, linked discovered-from its own ticket, is ready at once: bd doesn't
// count the link as blocking. It was written against that ticket's code, so it waits for it to
// merge as it would for a blocker; a source that is open and not running holds nothing.

// filesFollowUp is a worker that files follow-up id, discovered from its own ticket, then works on
// for a few ready checks before it does then.
func filesFollowUp(id string, then behaviour) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		w.beads.add(id, "follow-up of "+w.id, 1)
		w.beads.link(id, w.id, "discovered-from")
		time.Sleep(3 * readyPoll) // free slots meanwhile
		return then(w)
	}
}

// queuedBefore is the queue sizes the loop reported before ticket id merged.
func (s *runSink) queuedBefore(id string) []int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var l []int
	for _, ev := range s.events {
		if ev.Kind == EvClosed && ev.Ticket == id {
			break
		}
		if ev.Kind == EvQueue || ev.Kind == EvDispatch {
			l = append(l, ev.Queued)
		}
	}
	return l
}

// A's worker files F while it works. A slot is free, but F waits for A to merge, out of the queue,
// and then starts on a branch with A's code.
func TestFollowUpWaitsForItsRunningSourceToMerge(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "ticket A", 1)
		h.worker("A", filesFollowUp("F", finishes("a.txt")))
		h.worker("F", cutWith("A: add a.txt", "f.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"A", "F"}) {
			t.Errorf("merged %v, want A, then F\n%s", got, h.sink.text())
		}
		ev := h.sink.text()
		if strings.Count(ev, "F waits: filed by A's worker; waiting for it to merge") != 1 {
			t.Errorf("F's wait should be said once:\n%s", ev)
		}
		for _, n := range h.sink.queuedBefore("A") {
			if n != 0 {
				t.Errorf("queue sizes before A merged %v, want F left out of the queue", h.sink.queuedBefore("A"))
				break
			}
		}
	})
}

// A's worker files F, then closes A with no commit naming it: A is left unmerged, and F waits for it
// rather than start on a Base without A's code.
func TestFollowUpWaitsForItsUnmergedSource(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "ticket A", 1)
		h.worker("A", filesFollowUp("F", func(w *fakeWorker) AgentState {
			w.commitAs("a.txt", "add a.txt") // names no ticket
			w.close()
			return "idle"
		})) // no behaviour for F: a worker on it fails the test
		o, code := h.run()
		if code != ExitOK || !strings.HasPrefix(o.Final(), "READY_EMPTY") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := h.sink.dispatched(); !equal(got, []string{"A"}) {
			t.Errorf("dispatched %v, want A only", got)
		}
		if ev := h.sink.text(); !strings.Contains(ev,
			"F waits: filed by A's worker; A closed but not merged (CLOSED_WITHOUT_COMMIT)") {
			t.Errorf("events lack F waiting for A:\n%s", ev)
		}
		if st := h.statusOf("F"); st != "open" {
			t.Errorf("F is %s, want open", st)
		}
	})
}

// F was filed from A, which is deferred and not running: A may never run, so F starts at once, while
// C runs, as any ready ticket does.
func TestFollowUpOfANotRunningTicketStartsAtOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "ticket A", 1)
		h.beads.set("A", "deferred")
		h.beads.add("C", "ticket C", 1)
		h.beads.add("F", "follow-up of A", 2)
		h.beads.link("F", "A", "discovered-from")
		h.worker("C", busyFor(3*readyPoll, "c.txt"))
		h.worker("F", finishes("f.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"F", "C"}) {
			t.Errorf("merged %v, want F while C runs, then C\n%s", got, h.sink.text())
		}
		if ev := h.sink.text(); strings.Contains(ev, "F waits") {
			t.Errorf("F should not wait:\n%s", ev)
		}
	})
}

// A follow-up of a ticket an earlier run left unmerged is no ticket to start: nothing is ready to
// run, and the line says what holds it. The run, given the same tickets, holds it too.
func TestNothingToRunHoldsAFollowUpOfAnUnmergedTicket(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("U", "conflicted", 1, UnmergedLabel)
		h.beads.set("U", "closed")
		h.beads.add("F", "follow-up of U", 1)
		h.beads.link("F", "U", "discovered-from")
		n, err := CheckNothingToRun(t.Context(), h.cfg, h.beads, h.mem, h.mem)
		want := &NothingToRun{HeldFollowUps: 1, Unmerged: 1, Done: "READY_EMPTY after 0 tickets: nothing ready"}
		if err != nil || !reflect.DeepEqual(n, want) {
			t.Fatalf("got  %+v, %v\nwant %+v", n, err, want)
		}
		if n.Open() != 1 {
			t.Errorf("open %d, want 1", n.Open())
		}

		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 0 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if said := "F waits: filed by U's worker; U closed but not merged (" + earlierRun + ")"; !strings.Contains(
			h.sink.text(), said) {
			t.Errorf("the run didn't say %q:\n%s", said, h.sink.text())
		}
	})
}

// A scoped run whose follow-up waits for an unmerged ticket says so, as SCOPE_OPEN does.
func TestNothingToRunScopedSaysAFollowUpWaits(t *testing.T) {
	t.Parallel()
	c := nothingConfig(t, 0)
	b := newFakeBeads()
	b.add("U", "conflicted", 1, UnmergedLabel)
	b.set("U", "closed")
	b.add("F", "follow-up of U", 1)
	b.link("F", "U", "discovered-from")
	c.Ticket = "F"
	why := "filed by U's worker; U is closed but not merged"
	n := checkNothing(t, c, b)
	want := &NothingToRun{Scope: "F", NotDone: []NotDone{{"F", why}},
		Done: "READY_EMPTY after 0 tickets: nothing ready; " +
			"SCOPE_OPEN: F: its 0 subtickets are merged, but F itself is not done (" + why + ")"}
	if !reflect.DeepEqual(n, want) {
		t.Errorf("got  %+v\nwant %+v", n, want)
	}
}
