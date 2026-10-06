package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// asksAndGoes claims the ticket and asks the maintainer Q, then is gone from its tab, its ticket in
// progress: its tab closed while it waited, say.
func asksAndGoes(w *fakeWorker) AgentState {
	w.claim()
	w.ask("Q", "which way?")
	return StateGone
}

// answersThenFinishes has the maintainer answer Q, then finishes its own ticket.
func answersThenFinishes(file string) behaviour {
	return func(w *fakeWorker) AgentState {
		w.beads.set("Q", "closed")
		return finishes(file)(w)
	}
}

// A ticket back through bd ready once its question is answered, its earlier worker gone from its
// tab, has that worker's session resumed in a new tab, told the answer is in and to claim the ticket
// again, rather than a new worker starting over; its work merges.
func TestReturningTicketsGoneWorkerIsResumed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", asksAndGoes, finishes("a.txt"))
		h.worker("B", answersThenFinishes("b.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		starts := h.herdr.argsFor("A")
		if len(starts) != 2 || resumedWith(starts[0], testSession) || !resumedWith(starts[1], testSession) {
			t.Fatalf("starts %q; want a new worker, then one resuming %s", starts, testSession)
		}
		want := "Orchestra: your worker stopped, its tab gone, and this is your session resumed: A is yours again. " +
			"Your question Q is answered: read the answer with bd show Q. Claim A again with bd update A --claim, " +
			"and carry on with it where you left off, as your instructions say."
		if msg := starts[1][len(starts[1])-1]; msg != want {
			t.Errorf("the resumed worker was told %q\nwant %q", msg, want)
		}
		warn := "A   RESUMED: A is back, and its worker is gone from tab tab1; resuming its session " +
			testSession + " in a new tab (worktree " + h.worktree("A") + ")"
		if got := warnings(h, "A"); !equal(got, []string{warn}) {
			t.Errorf("warnings %q\nwant %q", got, warn)
		}
		if notes := h.beads.notesOf("A"); !strings.Contains(notes, "the ticket is back, and its worker in Herdr tab tab1 "+
			"is gone (worktree "+h.worktree("A")+"); its session "+testSession+" is resumed in a new tab") {
			t.Errorf("notes on A: %q", notes)
		}
		if got := closedIDs(h); !equal(got, []string{"B", "A"}) {
			t.Errorf("merged %v; want B, then A", got)
		}
	})
}

// A ticket whose session was resumed once in this run already gets a new worker when it comes back
// with its worker gone again, as it did before sessions were resumed: no session is resumed twice in
// a run.
func TestReturningTicketResumedOnceGetsANewWorker(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", vanishes, asksAndGoes, finishes("a.txt"))
		h.worker("B", answersThenFinishes("b.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		starts := h.herdr.argsFor("A")
		if len(starts) != 3 || !resumedWith(starts[1], testSession) || resumedWith(starts[2], testSession) {
			t.Fatalf("starts %q; want a new worker, one resuming %s, then a new one", starts, testSession)
		}
		if got := closedIDs(h); !equal(got, []string{"B", "A"}) {
			t.Errorf("merged %v; want B, then A", got)
		}
	})
}

// A ticket its worker deferred, undeferred before the next run, gets a new worker there, told what
// the earlier attempt left, though the earlier worker's session is recorded: a deferral is a stop.
func TestUndeferredTicketGetsANewWorker(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState {
			w.claim()
			w.commit("half.txt")
			time.Sleep(time.Minute)
			w.deferIt()
			return StateGone // its tab closed after, say
		}, finishes("a.txt"))
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		h.beads.set("A", "open") // bd undefer A
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		starts := h.herdr.argsFor("A")
		if len(starts) != 2 || resumedWith(starts[1], testSession) {
			t.Fatalf("starts %q; want a new worker in run 2", starts)
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v; want A", got)
		}
	})
}
