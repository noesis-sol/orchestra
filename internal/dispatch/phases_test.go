package dispatch

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// orchestra-rie6.1: the record that ends a ticket's part in the run (closed, deferred, or a warning that
// sets it aside) gives how long it spent in each phase since the run took it up, and its hand-backs.

// streamedRun runs h's loop with its event stream open, and returns the exit code and the run's records.
func streamedRun(t *testing.T, h *harness) (*Loop, int, []map[string]any) {
	t.Helper()
	o := h.loop()
	started := time.Now()
	o.log.Begin(h.repo, RunStart{Started: started, Repo: h.repo, Branch: "main", Concurrency: h.cfg.Concurrency})
	code := o.Run(context.Background())
	o.log.End(code)
	if err := o.log.Close(); err != nil {
		t.Fatal(err)
	}
	run := timeString(t, started)
	recs := slices.DeleteFunc(records(t, h.repo), func(r map[string]any) bool { return r["run"] != run })
	return o, code, recs
}

// ending is the record that ended ticket id's part in the run, failing the test unless there is one, and
// checks that no other record gives phases, hand-backs or carried.
func ending(t *testing.T, recs []map[string]any, id string) map[string]any {
	t.Helper()
	var end map[string]any
	for _, r := range recs {
		ends := r["ticket"] != "" && (r["kind"] == "closed" || r["kind"] == "deferred" ||
			r["kind"] == "warn" && r["aside"] == true)
		if !ends {
			has(t, r, nil, "phases", "hand_backs", "carried")
			continue
		}
		if r["ticket"] == id {
			end = r
		}
	}
	if end == nil {
		t.Fatalf("no record ends %s's part in the run", id)
	}
	return end
}

// phasesOf is record r's phases, in seconds by name, failing the test unless it has them.
func phasesOf(t *testing.T, r map[string]any) map[string]float64 {
	t.Helper()
	p, ok := r["phases"].(map[string]any)
	if !ok {
		t.Fatalf("%s record without phases: %v", r["kind"], r)
	}
	s := map[string]float64{}
	for k, v := range p {
		f, ok := v.(float64)
		if !ok || f < 0 {
			t.Errorf("%s record: phase %s is %#v, want seconds", r["kind"], k, v)
		}
		s[k] = f
	}
	return s
}

// checkPhases checks that record r gives the phases in want, at least as long as each says, and no
// other, and the hand-backs as resolve and fix.
func checkPhases(t *testing.T, r map[string]any, want map[string]float64, resolve, fix float64) {
	t.Helper()
	got := phasesOf(t, r)
	for k, least := range want {
		if s, ok := got[k]; !ok || s < least {
			t.Errorf("%s record: %s %v, want at least %v\n%v", r["kind"], k, s, least, r)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s record: phase %s, which the ticket never entered\n%v", r["kind"], k, r)
		}
	}
	hb, _ := r["hand_backs"].(map[string]any)
	if hb["resolve"] != resolve || hb["fix"] != fix {
		t.Errorf("%s record: hand_backs %v, want resolve %v, fix %v", r["kind"], r["hand_backs"], resolve, fix)
	}
}

// sleeps is b after its worker took d over it.
func sleeps(d time.Duration, b behaviour) behaviour {
	return func(w *fakeWorker) AgentState {
		time.Sleep(d)
		return b(w)
	}
}

// A ticket merged at once spent its time working, then merging: it never waited, nor was handed back.
func TestATicketMergedAtOnceGivesItsPhases(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Check = "true"
		h.beads.add("A", "first", 1)
		h.worker("A", sleeps(7*time.Minute, finishes("a.txt")))
		o, code, recs := streamedRun(t, h)
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		r := ending(t, recs, "A")
		has(t, r, map[string]any{"kind": "closed"}, "carried")
		checkPhases(t, r, map[string]float64{"working": 420, "merging": 0}, 0, 0)
		if w := phasesOf(t, r)["working"]; w > 420+60 {
			t.Errorf("working %v, want the 7m its worker took, and the reads after", w)
		}
	})
}

// A ticket whose check failed on its rebased branch was handed back to its worker to fix it, and the
// time its worker took over the fix is its fixing.
func TestATicketHandedBackForItsCheckGivesItsFixing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		h := checkHandBackHarness(t, dir, sleeps(3*time.Minute, commitsFix(dir, "admin_test.go", true)))
		o, code, recs := streamedRun(t, h)
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		r := ending(t, recs, "A")
		has(t, r, map[string]any{"kind": "closed"}, "carried")
		checkPhases(t, r, map[string]float64{"working": 0, "merging": 0, "fixing": 180}, 0, 1)
	})
}

// A ticket whose check fails after every hand-back is set aside with CHECKS_FAILED: that warning ends its
// part in the run, with each hand-back counted.
func TestATicketSetAsideForItsCheckGivesItsPhases(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		h := checkHandBackHarness(t, dir, commitsFix(dir, "admin_test.go", false), commitsFix(dir, "b.go", false))
		_, code, recs := streamedRun(t, h)
		if code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		r := ending(t, recs, "A")
		has(t, r, map[string]any{"kind": "warn", "aside": true})
		checkPhases(t, r, map[string]float64{"working": 0, "merging": 0, "fixing": 0}, 0, 2)
	})
}

// A ticket whose rebase stopped on conflicts was handed back to its worker to resolve them, and the time
// its worker took over them is its resolving.
func TestATicketHandedBackForAConflictGivesItsResolving(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := lateHarness(t)
		h.worker("A", sleeps(2*time.Minute, resolvesInMemory(h.mem)))
		o, code, recs := streamedRun(t, h)
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		r := ending(t, recs, "A")
		has(t, r, map[string]any{"kind": "closed"}, "carried")
		checkPhases(t, r, map[string]float64{"working": 0, "merging": 0, "resolving": 120}, 1, 0)
	})
}

// A ticket its worker deferred spent its time working.
func TestADeferredTicketGivesItsWorking(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A", sleeps(4*time.Minute, func(w *fakeWorker) AgentState { w.claim(); w.deferIt(); return "idle" }))
		_, code, recs := streamedRun(t, h)
		if code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		r := ending(t, recs, "A")
		has(t, r, map[string]any{"kind": "deferred"}, "carried")
		checkPhases(t, r, map[string]float64{"working": 240}, 0, 0)
	})
}

// A ticket carried over from the last run counts from when this run took it up, and says it was carried.
func TestACarriedTicketGivesItsPhasesFromThisRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		hooks := &hookReporter{}
		h.reporter = hooks
		h.cfg.Check = "true"
		h.beads.add("A", "first", 1)
		answered := make(chan struct{})
		h.worker("A", asksThenCarriesOn(hooks, answered, func(w *fakeWorker) AgentState {
			w.claim()
			w.commit("a.txt")
			w.close()
			hooks.report(w.wt, "Stop")
			return "idle"
		}))
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("run 1: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}

		// Between runs, an hour on: answered in A's tab, its worker finishes A.
		time.Sleep(time.Hour)
		h.beads.set("Q", "closed")
		close(answered)
		synctest.Wait()

		o, code, recs := streamedRun(t, h)
		if code != ExitOK || !slices.Equal(closedIDs(h), []string{"A"}) {
			t.Fatalf("run 2: exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		r := ending(t, recs, "A")
		has(t, r, map[string]any{"kind": "closed", "carried": true})
		checkPhases(t, r, map[string]float64{"working": 0, "merging": 0}, 0, 0)
		if w := phasesOf(t, r)["working"]; w >= time.Hour.Seconds() {
			t.Errorf("working %v, counted from before this run took A up", w)
		}
	})
}

// The phase clocks: the time in each phase entered, the hand-backs counted as one is entered, and
// nothing for a ticket the run didn't take up or whose part has ended.
func TestPhaseClocks(t *testing.T) {
	t.Parallel()
	var c phaseClocks
	at := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	c.carry("A")
	c.start("A", at)
	c.start("B", at)
	for _, step := range []struct {
		p     Phase
		after time.Duration
	}{
		{PhaseQueued, 10 * time.Minute}, {PhaseQueued, time.Minute}, {PhaseMerging, 2 * time.Minute},
		{PhaseFixing, 3 * time.Minute}, {PhaseWorking, 4 * time.Minute}, {PhaseMerging, time.Minute},
		{PhaseFixing, time.Minute}, {PhaseWorking, 5 * time.Minute}, {PhaseMerging, time.Minute},
	} {
		at = at.Add(step.after)
		c.enter("A", step.p, at)
	}
	got := c.end("A", at.Add(30*time.Second))
	want := &PhaseTimes{Fixes: 2, Carried: true}
	want.Spent[PhaseWorking] = 12 * time.Minute
	want.Spent[PhaseQueued] = 3 * time.Minute
	want.Spent[PhaseMerging] = 4*time.Minute + 30*time.Second
	want.Spent[PhaseFixing] = 4*time.Minute + 5*time.Minute
	want.Entered = [phaseCount]bool{PhaseWorking: true, PhaseQueued: true, PhaseMerging: true, PhaseFixing: true}
	if got == nil || *got != *want {
		t.Errorf("A's times %+v, want %+v", got, want)
	}
	if got := c.end("A", at); got != nil {
		t.Errorf("A's times once its part ended: %+v", got)
	}
	if got := c.end("C", at); got != nil {
		t.Errorf("times of a ticket not taken up: %+v", got)
	}
	if got := c.end("B", at); got == nil || got.Carried || got.Spent[PhaseWorking] != 28*time.Minute {
		t.Errorf("B's times %+v, want 28m working", got)
	}
}

// A record's phases are in seconds with one decimal, by the dashboard's names, in its order, leaving out
// those the ticket never entered; its hand-backs are both given, 0 included.
func TestPhasesAreRecordedInSeconds(t *testing.T) {
	t.Parallel()
	repo := t.TempDir()
	l, _ := openLog(t)
	l.Begin(repo, RunStart{Started: time.Now(), Concurrency: 1})
	times := &PhaseTimes{Carried: true}
	times.Spent[PhaseWorking], times.Entered[PhaseWorking] = 812*time.Second+440*time.Millisecond, true
	times.Spent[PhaseQueued], times.Entered[PhaseQueued] = 41*time.Second, true
	times.Spent[PhaseMerging], times.Entered[PhaseMerging] = 133*time.Second+160*time.Millisecond, true
	times.Spent[PhaseFixing], times.Entered[PhaseFixing], times.Fixes = 20*time.Millisecond, true, 1
	l.Record(Event{Kind: EvClosed, Ticket: "A", Times: times})
	l.End(0)
	b := read(t, filepath.Join(repo, project.RunPath(EventsName)))
	want := `"phases":{"working":812.4,"waiting":41.0,"merging":133.2,"fixing":0.0},` +
		`"hand_backs":{"resolve":0,"fix":1},"carried":true}`
	if !strings.Contains(b, want) {
		t.Errorf("records:\n%s\nwant in them: %s", b, want)
	}
}
