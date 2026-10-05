package dispatch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"
)

// A ticket set aside because its check failed is checked once more after Base moves on, as another
// ticket may have fixed what failed (see recheck).

// landsMeanwhile claims the ticket, has main move on while it works, so its branch is rebased and
// checked before it merges, commits file and closes it.
func landsMeanwhile(file string) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		w.git.commit("main", "landed on main: "+file+".landed", file+".landed")
		w.commit(file)
		w.close()
		return "idle"
	}
}

// countedCheck is a check command that notes each run in dir, in a file named after the worktree it
// runs in, which is named after its ticket, prints out and fails until dir holds the file fixed.
func countedCheck(dir, out string) string {
	return fmt.Sprintf(`echo run >> '%s'/"$(basename "$PWD")".runs; printf '%s'; test -f '%s'/fixed`, dir, out, dir)
}

// checkRuns is how many times countedCheck ran for ticket id.
func checkRuns(t *testing.T, dir, id string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, id+".runs"))
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "run\n")
}

// unmergedLabel reports whether ticket id is labelled UnmergedLabel.
func unmergedLabel(t *testing.T, h *harness, id string) bool {
	t.Helper()
	tk, err := h.beads.Show(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return HasLabel(tk, UnmergedLabel)
}

// orchestra-ckfm, from the run of 2026-10-04: orchestra-4wb.3's check failed on flaky tests in a
// package it doesn't touch, which tickets after it fixed, yet it stayed set aside for the rest of
// the run, holding up the tickets it blocked. Here A's check fails until B lands the fix; once B has
// merged, A is rebased and checked again, and merges, its unmerged label removed; D, which A blocks,
// runs after it.
func TestATicketSetAsideForAFailedCheckMergesOnceAnotherFixesIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = countedCheck(dir, "")
		h.beads.add("A", "first", 1)
		h.beads.add("B", "the fix", 2)
		h.beads.add("D", "builds on A", 3)
		h.beads.link("D", "A", "blocks")
		h.worker("A", landsMeanwhile("a.txt"))
		h.worker("B", func(w *fakeWorker) AgentState {
			if !unmergedLabel(t, h, "A") {
				t.Error("A should be labelled unmerged while it is set aside")
			}
			w.write(dir, "fixed", "")
			return finishes("b.txt")(w)
		})
		h.worker("D", finishes("d.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"B", "A", "D"}) {
			t.Errorf("merged %v, want B, then A checked again, then D, which waited for A\n%s", got, h.sink.text())
		}
		if unmergedLabel(t, h, "A") {
			t.Error("A's unmerged label should be removed as it merges")
		}
		if n := checkRuns(t, dir, "A"); n != 2 {
			t.Errorf("A's check ran %d times, want 2", n)
		}
		ev := h.sink.text()
		for _, want := range []string{
			"CHECKS_FAILED: A closed, but '" + h.cfg.Check + "' fails on wt/A rebased onto main; ",
			"/check.log); checked once more if main moves on in this run\n",
			"D waits: A closed but not merged (CHECKS_FAILED)",
			"RECHECK: A's check failed on main at ",
			", which has moved on since; rebasing wt/A and checking it once more\n",
			"'" + h.cfg.Check + "' passes on the rebased wt/A",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		if aside := o.setAside(); len(aside) != 0 {
			t.Errorf("set aside %v, want none: A merged", aside)
		}
		if done := eventsOf(h, EvDone); len(done) != 1 || done[0].Closed != 3 || done[0].SetAside != 0 {
			t.Errorf("done %+v, want 3 closed and none set aside", done)
		}
	})
}

// A ticket whose check fails again stays set aside, and isn't checked a third time once Base moves
// on again. Its check takes the only slot: C starts once it is done.
func TestATicketWhoseCheckFailsAgainStaysSetAside(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = countedCheck(dir, "") // never fixed
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.beads.add("C", "third", 3)
		h.worker("A", landsMeanwhile("a.txt"))
		h.worker("B", finishes("b.txt"))
		h.worker("C", finishes("c.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 3 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if got := closedIDs(h); !equal(got, []string{"B", "C"}) {
			t.Errorf("merged %v, want B and C", got)
		}
		if !unmergedLabel(t, h, "A") {
			t.Error("A should stay labelled unmerged")
		}
		if n := checkRuns(t, dir, "A"); n != 2 {
			t.Errorf("A's check ran %d times, want 2: once more after B merged, and no more after C did", n)
		}
		var why []string
		for _, ev := range eventsOf(h, EvWarn) {
			if ev.Ticket == "A" && ev.Aside {
				why = append(why, ev.Detail)
			}
		}
		if !equal(why, []string{"checks failed", "checks failed again"}) {
			t.Errorf("A set aside for %q, want its check failing, then failing again", why)
		}
		ev := h.sink.text()
		again := "CHECKS_FAILED: A closed, but '" + h.cfg.Check + "' fails again on wt/A rebased onto main; "
		recheck, failed, next := strings.Index(ev, "RECHECK: A"), strings.Index(ev, again), strings.Index(ev, "C dispatching")
		if recheck < 0 || failed < recheck || next < failed {
			t.Errorf("want A checked again and failing again before C starts:\n%s", ev)
		}
		if _, line, _ := strings.Cut(ev[max(failed, 0):], "; "); strings.Contains(strings.SplitN(line, "\n", 2)[0], "once more") {
			t.Errorf("the second CHECKS_FAILED promises another check: %s", line)
		}
		if aside := o.setAside(); !equal(aside, []string{"A"}) {
			t.Errorf("set aside %v, want A", aside)
		}
		if done := eventsOf(h, EvDone); len(done) != 1 || done[0].Closed != 2 || done[0].SetAside != 1 {
			t.Errorf("done %+v, want 2 closed and A set aside, counted once", done)
		}
	})
}

// A ticket the maintainer, or its worker, has taken up since it was set aside is left as it is
// rather than checked again: its branch isn't rebased under them.
func TestATicketTakenUpSinceItsCheckFailedIsNotCheckedAgain(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, why string
		touch     func(h *harness)
	}{
		{"reopened", "it is open now", func(h *harness) { h.beads.set("A", "open") }},
		{"branch moved", "wt/A has moved since its check failed", func(h *harness) {
			h.mem.commit("wt/A", "A: fixed by hand", "fix.txt")
		}},
		{"worker at work", "its worker is working in tab tab1", func(h *harness) {
			h.herdr.mu.Lock()
			defer h.herdr.mu.Unlock()
			h.herdr.agent("A").status = StateWorking
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				h := newTimedHarness(t)
				dir := t.TempDir()
				h.cfg.Check = countedCheck(dir, "")
				h.beads.add("A", "first", 1)
				h.beads.add("B", "the fix", 2)
				h.worker("A", landsMeanwhile("a.txt"))
				h.worker("B", func(w *fakeWorker) AgentState {
					tc.touch(h)
					w.write(dir, "fixed", "")
					return finishes("b.txt")(w)
				})
				o, code := h.run()
				if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
					t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
				}
				if got := closedIDs(h); !equal(got, []string{"B"}) {
					t.Errorf("merged %v, want B only", got)
				}
				if n := checkRuns(t, dir, "A"); n != 1 {
					t.Errorf("A's check ran %d times, want once", n)
				}
				if want := "  A is not checked again: " + tc.why + "; it stays set aside\n"; !strings.Contains(h.sink.text(), want) {
					t.Errorf("events lack %q:\n%s", want, h.sink.text())
				}
				if !unmergedLabel(t, h, "A") {
					t.Error("A should stay labelled unmerged")
				}
			})
		})
	}
}

// A ticket whose check failed in a package its own commits change isn't checked again: the next
// ticket to merge won't fix that.
func TestATicketWhoseCheckFailedInItsOwnCodeIsNotCheckedAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = countedCheck(dir, `FAIL\texample.com/m/internal/a\t0.512s\n`)
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", landsMeanwhile("internal/a/a.go"))
		h.worker("B", func(w *fakeWorker) AgentState {
			w.write(dir, "fixed", "") // would pass now
			return finishes("b.txt")(w)
		})
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if n := checkRuns(t, dir, "A"); n != 1 {
			t.Errorf("A's check ran %d times, want once", n)
		}
		ev := h.sink.text()
		if !strings.Contains(ev, "/check.log); not checked again in this run, as it failed in internal/a, "+
			"which A's own commits change\n") || strings.Contains(ev, "RECHECK") {
			t.Errorf("A should not be checked again:\n%s", ev)
		}
	})
}

// Asked to wind down, the run checks no ticket again: it only finishes the running ones.
func TestADrainingRunChecksNoTicketAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Check = countedCheck(dir, "")
		h.beads.add("A", "first", 1)
		h.beads.add("B", "the fix", 2)
		var o *Loop
		h.worker("A", landsMeanwhile("a.txt"))
		h.worker("B", func(w *fakeWorker) AgentState {
			o.Drain("from the dashboard")
			time.Sleep(time.Second) // heard before B is done
			w.write(dir, "fixed", "")
			return finishes("b.txt")(w)
		})
		o = h.loop()
		if code := o.Run(t.Context()); code != ExitOK || o.Final() != "DRAINED after 2 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if n := checkRuns(t, dir, "A"); n != 1 || strings.Contains(h.sink.text(), "RECHECK") {
			t.Errorf("A's check ran %d times, want once:\n%s", n, h.sink.text())
		}
	})
}

// What a failed check says decides whether it failed in the ticket's own code: a package or a file
// it names in a directory the ticket's commits change.
func TestOwnFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		f       checkFail
		wantOwn []string
	}{
		{"go test's FAIL", checkFail{said: []string{"FAIL\texample.com/m/internal/a\t0.512s"},
			dirs: []string{".", "internal/a"}}, []string{"internal/a"}},
		{"a build failure", checkFail{said: []string{"FAIL\texample.com/m/internal/a [build failed]"},
			dirs: []string{"internal/a"}}, []string{"internal/a"}},
		{"gotestsum's === FAIL", checkFail{said: []string{"=== FAIL: internal/a TestA (0.00s)"},
			dirs: []string{"internal/a"}}, []string{"internal/a"}},
		{"a lint error", checkFail{said: []string{"internal/a/a.go:3:1: exported A should have comment (revive)"},
			dirs: []string{"internal/a", "internal/b"}}, []string{"internal/a"}},
		{"a top-level file", checkFail{said: []string{"./main.go:3:1: undefined: x"},
			dirs: []string{"."}}, []string{"."}},
		{"elsewhere", checkFail{said: []string{"FAIL\texample.com/m/cmd/orchestra\t83.061s",
			"=== FAIL: cmd/orchestra TestTerminalShowsThePlan (2.00s)", "--- FAIL: TestTerminalShowsThePlan (2.00s)"},
			dirs: []string{".", "internal/a"}}, nil},
		{"a package beside", checkFail{said: []string{"FAIL\texample.com/m/internal/ab\t0.1s"},
			dirs: []string{"internal/a"}}, nil},
		{"the top level isn't every package", checkFail{said: []string{"FAIL\texample.com/m/internal/a\t0.1s"},
			dirs: []string{"."}}, nil},
		{"no line saying what failed", checkFail{said: []string{"internal/a/a.go:3:1: x"}, saidEnd: true,
			dirs: []string{"internal/a"}}, nil},
		{"no directories", checkFail{said: []string{"FAIL\texample.com/m/internal/a\t0.1s"}}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.f.ownFailures(); !equal(got, tc.wantOwn) {
				t.Errorf("ownFailures() = %q, want %q", got, tc.wantOwn)
			}
		})
	}
}
