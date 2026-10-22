package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// The merge train two tickets deep (merge_train 2): while A holds the merge queue and its check runs,
// B, next in line, is rebased onto A's branch as it is checked, and C, behind it, onto B's train once B
// is rebased. When A and B merge as they were checked, C merges in its turn with no further check; when
// B's check ahead of its turn fails, C is put back as its worker left it, and rebased and checked in
// its turn as any other.

// deepTrainCheck counts each ticket's runs in dir, as countedCheck does. A passes once C's check has
// started, on top of B's train, waiting for it for up to 10 seconds; B passes, unless dir has B.fails:
// it fails then, once C's check has started; C passes.
func deepTrainCheck(dir string) string {
	return fmt.Sprintf(`id=$(basename "$PWD"); echo run >> '%[1]s'/$id.runs; `+
		`if [ "$id" = A ] || { [ "$id" = B ] && [ -f '%[1]s'/B.fails ]; }; then `+
		`i=0; while [ ! -f '%[1]s'/C.runs ] && [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done; `+
		`test -f '%[1]s'/C.runs || exit 1; fi; `+
		`if [ "$id" = B ]; then test ! -f '%[1]s'/B.fails; fi`, dir)
}

// deepTrainScenario runs A, B and C with the train at 2, B's check passing or not, and returns the
// harness, the git it ran on, the loop, the run's exit code and where the checks counted their runs. A
// lands a commit on main before it closes, so all three are rebased; B finishes a minute in and C half
// a minute later, while A merges, A's check held until C waits in line (see trainGit).
func deepTrainScenario(t *testing.T, bPasses bool) (*harness, *trainGit, *Loop, int, string) {
	h := newTimedHarness(t)
	dir := t.TempDir()
	if !bPasses {
		if err := os.WriteFile(filepath.Join(dir, "B.fails"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.cfg.Concurrency = 3
	h.cfg.MergeTrain = 2
	h.cfg.CheckHandBacks = 0
	h.cfg.Check = deepTrainCheck(dir)
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.beads.add("C", "third", 3)
	h.worker("A", func(w *fakeWorker) AgentState {
		time.Sleep(10 * time.Second) // B's and C's worktrees are made from main before the commit lands
		w.claim()
		w.commit("a.txt")
		w.git.commit("main", "landed on main: landed.txt", "landed.txt")
		w.close()
		return "idle"
	})
	h.worker("B", busyFor(time.Minute, "b.txt"))
	h.worker("C", busyFor(90*time.Second, "c.txt"))
	queued := make(chan struct{})
	g := &trainGit{fakeGit: h.mem, queued: queued, onto: map[string][]string{}}
	o := h.loop()
	o.merger, o.checkout = g, g
	o.SetSink(queuedSink{Sink: h.sink, on: "C", once: &sync.Once{}, queued: queued})
	return h, g, o, o.Run(context.Background()), dir
}

func TestTheTrainChecksTwoTicketsAheadEachOnTheOneBeforeIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, g, o, code, dir := deepTrainScenario(t, true)
		ev := h.sink.text()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A", "B", "C"}) {
			t.Fatalf("merged %v, want A, B then C\n%s", got, ev)
		}
		a, b := commitOf(h.mem, "main", "A: add a.txt"), commitOf(h.mem, "main", "B: add b.txt")
		if got := g.rebasedOnto(h.worktree("B")); !equal(got, []string{a}) {
			t.Errorf("B was rebased onto %v, want once, onto A's branch as it was checked and merged (%s)", got, a)
		}
		if got := g.rebasedOnto(h.worktree("C")); !equal(got, []string{b}) {
			t.Errorf("C was rebased onto %v, want once, onto B's train as it was checked and merged (%s)", got, b)
		}
		for _, want := range []string{
			"checking wt/B ahead of its turn to merge, rebased onto wt/A as it is checked\n",
			"checking wt/C ahead of its turn to merge, rebased onto wt/B as it is checked\n",
			"'" + h.cfg.Check + "' passes on wt/C ahead of its turn, on top of wt/B\n",
			"wt/B was checked ahead of its turn on top of wt/A, which merged as it was checked: " +
				"merging it with no further check\n",
			"wt/C was checked ahead of its turn on top of wt/B, which merged as it was checked: " +
				"merging it with no further check\n",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		for _, id := range []string{"A", "B", "C"} {
			if n := checkRuns(t, dir, id); n != 1 {
				t.Errorf("%s's check ran %d times, want once", id, n)
			}
		}
		main := h.mem.log("main")
		c := commitOf(h.mem, "main", "C: add c.txt")
		if !strings.HasPrefix(main, c+" C: add c.txt\n"+b+" B: add b.txt\n"+a+" A: add a.txt\n") {
			t.Errorf("main should end with A, then B, then C on top of it:\n%s", main)
		}
	})
}

func TestTheTicketBehindIsCheckedInItsTurnWhenTheOneAheadFailsAhead(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h, g, o, code, dir := deepTrainScenario(t, false)
		ev := h.sink.text()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A", "C"}) {
			t.Fatalf("merged %v, want A then C\n%s", got, ev)
		}
		if got := o.asideList(); !equal(got, []string{"B"}) {
			t.Errorf("set aside %v, want B", got)
		}
		main := h.mem.log("main")
		if strings.Contains(main, "B: add") || !strings.Contains(main, "A: add a.txt") ||
			!strings.Contains(main, "C: add c.txt") {
			t.Errorf("main should have A and C without B:\n%s", main)
		}
		onto, a := g.rebasedOnto(h.worktree("C")), commitOf(h.mem, "main", "A: add a.txt")
		if len(onto) != 2 || onto[0] == "main" || onto[0] == a || onto[1] != a {
			t.Errorf("C was rebased onto %v, want onto B's train ahead of its turn, then onto main's %s", onto, a)
		}
		for _, want := range []string{
			"checking wt/C ahead of its turn to merge, rebased onto wt/B as it is checked\n",
			" on wt/B ahead of its turn, on top of wt/A (output is in ",
			"wt/C was checked ahead of its turn on top of wt/B, which did not merge as it was checked: " +
				"put back as its worker left it\n",
			"rebased wt/C onto main, which moved on while it ran\n",
			"'" + h.cfg.Check + "' passes on the rebased wt/C\n",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		if n := checkRuns(t, dir, "C"); n != 2 {
			t.Errorf("C's check ran %d times, want 2: ahead of its turn, and in it", n)
		}
	})
}
