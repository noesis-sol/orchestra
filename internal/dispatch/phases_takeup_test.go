package dispatch

import (
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// orchestra-qw5v.10: a ticket's clock carries on across the run's take-ups of it, asking a question
// included, and the wait for the answer counts in no phase; a recheck that leaves its ticket as it is
// leaves no clock running.

// A works 5m and asks; B takes 30m, and the maintainer answers meanwhile; A comes back, works 3m more and
// closes. Its closed record counts both periods' working, not the wait between, and the tokens its worker
// used from when the run first took it up.
func TestAnAskedTicketsRecordCountsItsWorkBeforeTheQuestion(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Check = "true"
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A",
			sleeps(5*time.Minute, func(w *fakeWorker) AgentState { w.claim(); w.ask("Q", "which way?"); return "idle" }),
			sleeps(3*time.Minute, finishes("a.txt"))) // once told the answer is in
		h.worker("B", sleeps(30*time.Minute, func(w *fakeWorker) AgentState {
			w.beads.set("Q", "closed") // the maintainer answers meanwhile
			return finishes("b.txt")(w)
		}))
		a := session("a")
		rep := &tokensReporter{
			sessions: map[string][]Session{h.worktree("A"): {a}},
			used:     map[string]Tokens{a.Transcript: {"claude-opus-5-5": {Input: 1, Output: 2, CacheWrite: 3, CacheRead: 4}}},
		}
		h.reporter = rep
		started := time.Now()
		o, code, recs := streamedRun(t, h)
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		r := ending(t, recs, "A")
		has(t, r, map[string]any{"kind": "closed"}, "carried")
		checkPhases(t, r, map[string]float64{"working": 8 * 60, "merging": 0}, 0, 0)
		if w := phasesOf(t, r)["working"]; w >= 30*60 {
			t.Errorf("working %v: the wait for the answer counted as working", w)
		}
		want := map[string]any{"claude-opus-5-5": model(1, 2, 3, 4)}
		if got := tokensOn(t, r); !reflect.DeepEqual(got, want) {
			t.Errorf("A's tokens %v, want %v", got, want)
		}
		rep.mu.Lock()
		defer rep.mu.Unlock()
		for _, since := range rep.since {
			if since.Sub(started) >= 5*time.Minute { // A first at once, B as A asked
				t.Errorf("tokens counted from %v after the run started: A's from its second take-up",
					since.Sub(started))
			}
		}
	})
}

// A's check fails and it is set aside; once B merges, A's worker works again in its tab, so A is not
// checked again; C, which B blocks, starts beside, and 30m later the maintainer squash-merges wt/A by
// hand. A's merged-by-hand record counts none of the time since as working.
func TestASkippedRecheckLeavesNoClockForAMergeByHand(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const skipped = "  A is not checked again: its worker is working in tab " // A and B start side by side
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = failsOnlyFor("A", countedCheck(dir, ""))
		h.beads.add("A", "first", 1)
		h.beads.add("B", "beside", 2)
		h.beads.add("C", "later", 3)
		h.beads.link("C", "B", "blocks")
		h.cfg.Concurrency = 2 // a slot for A's recheck beside C
		h.worker("A", landsMeanwhile("a.txt"))
		h.worker("B", afterSetAside(h, "A", func(w *fakeWorker) AgentState {
			h.herdr.mu.Lock()
			h.herdr.agent("A").status = StateWorking
			h.herdr.mu.Unlock()
			return finishes("b.txt")(w)
		}))
		h.worker("C", func(w *fakeWorker) AgentState {
			for i := 0; i < 3600 && !strings.Contains(h.sink.text(), skipped); i++ { // an hour at most: see below
				time.Sleep(time.Second)
			}
			time.Sleep(30 * time.Minute)
			h.mem.commit("main", "A: add a.txt, squashed", "a.txt")
			time.Sleep(2 * time.Minute) // the run polls meanwhile
			return finishes("c.txt")(w)
		})
		o, code, recs := streamedRun(t, h)
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{skipped, "  A, set aside in this run, is on main now ("} {
			if !strings.Contains(ev, want) {
				t.Fatalf("events lack %q:\n%s", want, ev)
			}
		}
		r := ending(t, recs, "A")
		has(t, r, map[string]any{"kind": "closed"})
		if p, ok := r["phases"].(map[string]any); ok {
			t.Errorf("A's merged-by-hand record gives phases %v: the skipped recheck's clock ran on", p)
		}
	})
}

// A clock carries on across take-ups, counting nothing while paused, and keeps when the run first took
// its ticket up; a paused clock ends with what it counted; a dropped one ends with nothing.
func TestPhaseClocksCarryOnAcrossTakeUps(t *testing.T) {
	t.Parallel()
	var c phaseClocks
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	first := at
	c.start("A", at)
	c.start("B", at)
	at = at.Add(5 * time.Minute)
	c.pause("A", at) // A asks
	c.pause("A", at.Add(time.Minute))
	at = at.Add(30 * time.Minute)
	if taken, ok := c.takenUp("A"); !ok || !taken.Equal(first) {
		t.Errorf("A taken up at %v, %v while it waits; want %v", taken, ok, first)
	}
	c.start("A", at) // answered, A comes back
	at = at.Add(3 * time.Minute)
	c.enter("A", PhaseQueued, at)
	at = at.Add(time.Minute)
	c.enter("A", PhaseMerging, at)
	if taken, _ := c.takenUp("A"); !taken.Equal(first) {
		t.Errorf("A taken up at %v once back, want %v", taken, first)
	}
	got := c.end("A", at.Add(2*time.Minute))
	want := &PhaseTimes{}
	want.Spent[PhaseWorking] = 8 * time.Minute
	want.Spent[PhaseQueued] = time.Minute
	want.Spent[PhaseMerging] = 2 * time.Minute
	want.Entered = [phaseCount]bool{PhaseWorking: true, PhaseQueued: true, PhaseMerging: true}
	if got == nil || *got != *want {
		t.Errorf("A's times %+v, want %+v", got, want)
	}

	c.pause("B", first.Add(4*time.Minute)) // waits for a ticket to merge, and its part ends meanwhile
	if got := c.end("B", at); got == nil || got.Spent[PhaseWorking] != 4*time.Minute {
		t.Errorf("B's times %+v, want 4m working", got)
	}

	c.start("C", at) // a recheck that leaves C as it is
	c.drop("C")
	if _, ok := c.takenUp("C"); ok {
		t.Error("C's clock runs once dropped")
	}
	if got := c.end("C", at.Add(time.Hour)); got != nil {
		t.Errorf("C's times once its clock was dropped: %+v", got)
	}
}
