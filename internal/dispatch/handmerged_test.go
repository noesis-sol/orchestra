package dispatch

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
)

// orchestra-nr1v.9, from tutti's run of 2026-10-19: tutti-nrg.8.6 was set aside CHECKS_FAILED, and
// the maintainer merged it by hand as the run went on; the run never noticed, and its report still
// listed it as set aside. Here A's check fails, and B's worker merges wt/A into main by hand: the run
// says so, counts A merged rather than set aside, removes its unmerged label and starts D, which A
// blocks, without checking A again.
func TestATicketSetAsideAndMergedByHandMidRunCountsMerged(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = failsOnlyFor("A", countedCheck(dir, "")) // never fixed
		h.beads.add("A", "first", 1)
		h.beads.add("B", "beside", 2)
		h.beads.add("D", "builds on A", 3)
		h.beads.link("D", "A", "blocks")
		h.worker("A", landsMeanwhile("a.txt"))
		h.worker("B", afterSetAside(h, "A", func(w *fakeWorker) AgentState {
			if out, err := w.git.FastForward(context.Background(), "", "wt/A"); err != nil {
				t.Errorf("merging wt/A by hand: %v: %s", err, out)
			}
			time.Sleep(2 * time.Minute) // the run polls meanwhile
			return finishes("b.txt")(w)
		}))
		h.worker("D", finishes("d.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		if got := closedIDs(h); !equal(got, []string{"A", "B", "D"}) {
			t.Errorf("closed %v, want A, merged by hand, then B, then D, which waited for A\n%s", got, ev)
		}
		want := "  A, set aside in this run, is on main now (" // then the commit naming A
		tail := "): merged by hand, so no longer set aside; its '" + UnmergedLabel + "' label is removed\n"
		if i := strings.Index(ev, want); i < 0 || !strings.Contains(ev[i:], tail) || strings.Count(ev, want) != 1 {
			t.Errorf("events should say once that A was merged by hand:\n%s", ev)
		}
		if h.sink.tagged("RECHECK") || checkRuns(t, dir, "A") != 1 {
			t.Errorf("A's check ran %d times, want once: merged by hand, it isn't checked again\n%s",
				checkRuns(t, dir, "A"), ev)
		}
		if unmergedLabel(t, h, "A") {
			t.Error("A's unmerged label should be removed once it is merged by hand")
		}
		if aside := o.asideList(); len(aside) != 0 {
			t.Errorf("set aside %v, want none: A merged by hand", aside)
		}
		if closed := o.closedInRun(); !equal(closed, []string{"A", "B", "D"}) {
			t.Errorf("the reviewer reads %v closed in this run, want A, B and D", closed)
		}
		for _, e := range eventsOf(h, EvClosed) {
			if e.Ticket == "A" && !strings.HasSuffix(e.Detail, " merged by hand") {
				t.Errorf("A's row says %q, want it merged by hand", e.Detail)
			}
		}
		if done := eventsOf(h, EvDone); len(done) != 1 || done[0].Closed != 3 || done[0].SetAside != 0 {
			t.Errorf("done %+v, want 3 closed and none set aside", done)
		}
	})
}

// A ticket set aside not to be checked again, its check failed in its own code, is found merged by
// hand all the same, here as the last ticket settles: the end line counts it merged.
func TestATicketNotToBeCheckedAgainMergedByHandCountsMerged(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = failsOnlyFor("A", countedCheck(dir, `FAIL\texample.com/m/internal/a\t0.512s\n`))
		h.beads.add("A", "first", 1)
		h.beads.add("B", "beside", 2)
		h.worker("A", landsMeanwhile("internal/a/a.go"))
		h.worker("B", afterSetAside(h, "A", func(w *fakeWorker) AgentState {
			w.claim()
			w.commit("b.txt")
			w.close()
			if out, err := w.git.FastForward(context.Background(), "", "wt/A"); err != nil { // as B settles
				t.Errorf("merging wt/A by hand: %v: %s", err, out)
			}
			return "idle"
		}))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if !strings.Contains(h.sink.text(), "  A, set aside in this run, is on main now (") {
			t.Errorf("events should say A was merged by hand:\n%s", h.sink.text())
		}
		if done := eventsOf(h, EvDone); len(done) != 1 || done[0].Closed != 2 || done[0].SetAside != 0 {
			t.Errorf("done %+v, want 2 closed and none set aside", done)
		}
	})
}

// A run asked to wind down drops the tickets waiting to be checked again, naming each: Base moving on
// later in the run checks none of them.
func TestADrainNamesTheTicketsItKeepsFromACheckOnceMore(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = countedCheck(dir, "")
		h.beads.add("A", "first", 1)
		h.beads.add("B", "the fix", 2)
		var o *Loop
		h.worker("A", landsMeanwhile("a.txt"))
		h.worker("B", afterSetAside(h, "A", func(w *fakeWorker) AgentState {
			o.Drain("from the dashboard")
			time.Sleep(time.Minute) // heard before B is done
			w.write(dir, "fixed", "")
			return finishes("b.txt")(w)
		}))
		o = h.loop()
		if code := o.Run(t.Context()); code != ExitOK || o.Final() != "DRAINED after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		if !strings.Contains(ev, "/check.log); checked once more if main moves on in this run\n") {
			t.Errorf("A should have been promised a check once more:\n%s", ev)
		}
		dropped := "  A is not checked again in this run, as the run winds down; it stays set aside\n"
		drain, said := strings.Index(ev, "DRAIN: "), strings.Index(ev, dropped)
		if drain < 0 || said < drain || strings.Count(ev, dropped) != 1 {
			t.Errorf("the drain should say once that A won't be checked again:\n%s", ev)
		}
		if n := checkRuns(t, dir, "A"); n != 1 || h.sink.tagged("RECHECK") {
			t.Errorf("A's check ran %d times, want once:\n%s", n, ev)
		}
	})
}

// A ticket set aside as the run winds down isn't promised a check once more.
func TestATicketSetAsideAsTheRunWindsDownIsNotPromisedACheckOnceMore(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = countedCheck(dir, "")
		h.beads.add("A", "first", 1)
		var o *Loop
		h.worker("A", func(w *fakeWorker) AgentState {
			o.Drain("from the dashboard")
			time.Sleep(time.Minute) // heard before A is done
			return landsMeanwhile("a.txt")(w)
		})
		o = h.loop()
		if code := o.Run(t.Context()); code != ExitOK || o.Final() != "DRAINED after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		if !strings.Contains(ev, "/check.log); not checked again in this run, as it winds down\n") ||
			strings.Contains(ev, "checked once more if") {
			t.Errorf("A's CHECKS_FAILED should say it isn't checked again:\n%s", ev)
		}
	})
}

// A run that stops names, as it ends, the tickets it leaves that were to be checked again.
func TestAStoppedRunNamesTheTicketsItKeepsFromACheckOnceMore(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = failsOnlyFor("A", countedCheck(dir, ""))
		var dirty atomic.Bool
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", landsMeanwhile("a.txt"))
		h.worker("B", afterSetAside(h, "A", func(w *fakeWorker) AgentState {
			dirty.Store(true) // B's merge stops the run
			return finishes("b.txt")(w)
		}))
		o := h.loop()
		o.checkout = dirtyCheckout{fakeGit: h.mem, dirty: &dirty}
		if code := o.Run(t.Context()); code != ExitDirty {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		dropped := "  A is not checked again in this run, as the run stopped; it stays set aside\n"
		said, stop := strings.Index(ev, dropped), strings.LastIndex(ev, "DIRTY_TREE: ")
		if said < 0 || stop < said || strings.Count(ev, dropped) != 1 {
			t.Errorf("the run should say once, before its stop line, that A won't be checked again:\n%s", ev)
		}
	})
}
