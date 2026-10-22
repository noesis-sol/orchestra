package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// Herdr can lose sight of a worker still at work for a moment: a Claude Code the worker runs in its
// pane takes the pane's agent over through Herdr's hook, and as it ends the pane may show no agent
// until the worker's own next hook (tutti's run of 2026-10-18, where tutti-nrg.64's worker, resumed,
// was taken for gone and stopped the run PAUSED, though the next run found it in its tab). A worker
// the terminal shows nowhere is read again for goneGrace before it settles, gone.

// outOfSight claims the ticket and works on, the terminal showing it nowhere a minute in, neither under
// its name nor in its pane, for d; then it is back, unnamed in its pane, and a minute later goes on
// as then does.
func outOfSight(h *fakeHerdr, d time.Duration, then behaviour) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		time.Sleep(time.Minute)
		var a *fakeAgent
		unnamed(h, w.id, func(_ *fakeHerdr, gone *fakeAgent) { a, gone.status = gone, StateGone })
		time.Sleep(d)
		h.mu.Lock()
		a.status = StateWorking
		h.mu.Unlock()
		time.Sleep(time.Minute)
		return then(w)
	}
}

// orchestra-rbos: a worker resumed in a new tab that the terminal shows nowhere for less than
// goneGrace is followed to the end of its ticket, which merges, rather than taken for gone a second
// time, which stops the run (PAUSED).
func TestResumedWorkerOutOfSightForAMomentIsFollowed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.worker("A", vanishes, outOfSight(h.herdr, goneGrace-5*time.Second, finishes("a.txt")))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"A"}) {
			t.Errorf("merged %v; want A", got)
		}
		if starts := h.herdr.argsFor("A"); len(starts) != 2 || !resumedWith(starts[1], testSession) {
			t.Errorf("starts %q; want a new worker, then one resuming %s", starts, testSession)
		}
		ev := h.sink.text()
		if n := strings.Count(ev, "  A settled: its worker is gone\n"); n != 1 {
			t.Errorf("A settled gone %d times; want once, its first worker:\n%s", n, ev)
		}
		if !strings.Contains(ev, "  A's worker was without its name in tab tab2; named it A again\n") {
			t.Errorf("A's resumed worker wasn't named again:\n%s", ev)
		}
		if got := h.sink.of(EvHold); len(got) != 0 {
			t.Errorf("held for %q", got)
		}
	})
}

// A worker the terminal shows nowhere for less than goneGrace is followed in its tab: its session
// isn't resumed beside it.
func TestWorkerOutOfSightForAMomentIsNotResumed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.worker("A", outOfSight(h.herdr, goneGrace-5*time.Second, finishes("a.txt")))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if starts := h.herdr.argsFor("A"); len(starts) != 1 {
			t.Errorf("started %d workers on A; want its one: %q", len(starts), starts)
		}
		if got := warnings(h, "A"); len(got) != 0 {
			t.Errorf("A warned about:\n%s", strings.Join(got, "\n"))
		}
	})
}

// One the terminal shows nowhere for longer than goneGrace is gone: its session is resumed.
func TestWorkerOutOfSightPastTheGraceIsResumed(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		withSession(h, "A")
		h.beads.add("A", "first", 1)
		h.worker("A", outOfSight(h.herdr, goneGrace+10*time.Second, quits), finishes("a.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if starts := h.herdr.argsFor("A"); len(starts) != 2 || !resumedWith(starts[1], testSession) {
			t.Errorf("starts %q; want a new worker, then one resuming %s", starts, testSession)
		}
	})
}
