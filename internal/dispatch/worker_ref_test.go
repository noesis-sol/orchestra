package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// Herdr can lose sight of a worker still in its tab for a moment (see goneGrace). Every path that
// follows a worker decides it is gone through one check, presence, which takes it for gone only once
// the terminal has shown it nowhere for goneGrace: not only the settle loop, but the asked tickets
// followed (followAsked), the workers left open (followLeft) and those carried over from the last run
// (carriedStands) (orchestra-4n6j.10).

// refOf is where a test's worker on ticket id is: tab-<id>, /wt/<id>, named id.
func refOf(id string) workerRef {
	return workerRef{tab: "tab-" + id, wt: "/wt/" + id, agent: id}
}

// blinks has the terminal show ticket id's worker nowhere, neither under its name nor in its pane, for
// d, as Herdr does for a moment; then it is back in its pane, unnamed, as back.
func blinks(h *fakeHerdr, id string, d time.Duration, back AgentState) {
	var a *fakeAgent
	unnamed(h, id, func(_ *fakeHerdr, gone *fakeAgent) { a, gone.status = gone, StateGone })
	time.Sleep(d)
	h.mu.Lock()
	a.status = back
	h.mu.Unlock()
}

// spanningAPoll is how long a followed worker is out of sight in the tests of the follow paths, which
// read it once a poll of the run's (readyPoll): longer than a poll, so that one finds it so whatever
// its phase, yet back by the poll after the next, which would find it out of sight past goneGrace.
const spanningAPoll = readyPoll + 10*time.Second

// An asked ticket's worker that claims it again once answered, and that the terminal shows nowhere
// for a moment as it does, is adopted once it is back: its session isn't resumed beside it.
func TestAskedWorkerOutOfSightForAMomentIsAdoptedNotResumed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		r := withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		answered := make(chan struct{})
		h.worker("A", asksThenCarriesOn(&r.hookReporter, answered, func(w *fakeWorker) AgentState {
			time.Sleep(time.Minute)
			w.shows("working")
			w.claim()
			blinks(h.herdr, "A", spanningAPoll, StateWorking)
			time.Sleep(5 * time.Minute)
			w.commit("a.txt")
			w.close()
			return "idle"
		}))
		h.worker("B", answersAfter(time.Minute+7*time.Second, answered, time.Hour, "b.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if starts := h.herdr.argsFor("A"); len(starts) != 1 {
			t.Errorf("started %d workers on A; want its one, adopted: %q", len(starts), starts)
		}
		if got := warnings(h, "A"); len(got) != 0 {
			t.Errorf("A warned about:\n%s", strings.Join(got, "\n"))
		}
		ev := h.sink.text()
		if !strings.Contains(ev, "  A's worker was without its name in tab tab1; named it A again\n") ||
			!strings.Contains(ev, "ANSWERED: A was claimed again in tab tab1 after Q") {
			t.Errorf("A's worker wasn't found back in its tab and adopted:\n%s", ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A", "B"}) {
			t.Errorf("merged %v; want A, then B", got)
		}
	})
}

// A worker left open as its ticket was set aside, that the terminal shows nowhere for a moment, is
// still followed: closed in its tab later, its ticket is merged (LATE_CLOSE).
func TestLeftOpenWorkerOutOfSightForAMomentIsStillFollowed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Concurrency = 2
		h.beads.add("A", "left open", 1)
		h.beads.add("C", "beside", 2)
		h.worker("A", leavesOpen)
		h.worker("C", func(w *fakeWorker) AgentState {
			time.Sleep(startGrace + 2*time.Minute) // A is set aside meanwhile
			blinks(h.herdr, "A", spanningAPoll, StateIdle)
			time.Sleep(2 * time.Minute)
			closesInTab(h, "A", "a.txt")
			time.Sleep(2 * time.Minute)
			return finishes("c.txt")(w)
		})
		o, code := h.run()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		if strings.Contains(ev, "it is no longer followed") {
			t.Errorf("A's worker, out of sight for a moment, was no longer followed:\n%s", ev)
		}
		if !h.sink.tagged("LATE_CLOSE") || !equal(closedIDs(h), []string{"A", "C"}) {
			t.Errorf("merged %v; want A as a late close, then C:\n%s", closedIDs(h), ev)
		}
	})
}

// A worker the last run left running, its ticket in progress and no session to resume, that the
// terminal shows nowhere for a moment as the next run starts, is carried over, not dropped as gone
// (WORKER_GONE).
func TestCarriedWorkerOutOfSightForAMomentIsCarriedOver(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.beads.add("C", "third", 3)
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); return "idle" })
		h.worker("C", finishes("c.txt"))
		if o, code := h.run(); code != ExitStuck {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}

		blinked := make(chan struct{})
		go func() {
			defer close(blinked)
			blinks(h.herdr, "A", goneGrace-10*time.Second, StateIdle)
		}()
		o, code := h.run()
		<-blinked
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := warnings(h, "A"); len(got) != 0 {
			t.Errorf("A warned about:\n%s", strings.Join(got, "\n"))
		}
		ev := h.sink.text()
		if !strings.Contains(ev, "  carried over from the last run: A (left running: PAUSED)\n") {
			t.Errorf("A wasn't carried over:\n%s", ev)
		}
		if got := saved(t, h); len(got) != 1 || got[0].Ticket != "A" {
			t.Errorf("saved for the next run: %+v; want A", got)
		}
		if got := closedIDs(h); !equal(got, []string{"C"}) {
			t.Errorf("merged %v; want C", got)
		}
	})
}
