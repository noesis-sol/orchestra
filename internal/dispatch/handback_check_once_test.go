package dispatch

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
)

// A ticket handed back to its worker, to resolve its rebase or fix its check, is checked once when
// the worker returns: on the Base it merges onto, rebased first when Base moved on meanwhile, rather
// than on the Base it was rebased onto before the hand-back and then again after a rebase.

// countedConflictCheck is conflictHarness's check, counting its runs in dir as countedCheck does.
func countedConflictCheck(dir string) string {
	return fmt.Sprintf(`echo run >> '%s'/"$(basename "$PWD")".runs; ! grep -q '<<<<<<<' shared.txt`, dir)
}

func TestAResolvedRebaseIsCheckedOnceOnTheBaseItMergesOnto(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		lands      []string // the file and its content that land on main while the worker resolves, if any
		more       []behaviour
		wantShared string
	}{
		{"with Base unmoved", nil, nil, "main\nA\n"},
		{"with Base moved on", []string{"other.txt", "other\n"}, nil, "main\nA\n"},
		{"with a conflict on the new Base", []string{"shared.txt", "main 2\n"},
			[]behaviour{resolvesWith("main 2\nA\n")}, "main 2\nA\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			h := conflictHarness(t)
			h.cfg.Check = countedConflictCheck(dir)
			h.beads.add("A", "first", 1)
			h.worker("A", conflicting(h.repo), func(w *fakeWorker) AgentState {
				if tc.lands != nil {
					landsOnMain(w, h.repo, tc.lands[0], tc.lands[1])
				}
				return resolvesWith("main\nA\n")(w)
			})
			h.worker("A", tc.more...)
			o, code := h.run()
			if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
				t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			ev := h.sink.text()
			if !h.sink.has(EvClosed, "A") || h.sink.warned("MERGE_CONFLICT", "") {
				t.Errorf("events:\n%s", h.sink.text())
			}
			if n := checkRuns(t, dir, "A"); n != 1 {
				t.Errorf("A's check ran %d times, want once:\n%s", n, ev)
			}
			if n := strings.Count(ev, "RESOLVING: wt/A"); n != 1+len(tc.more) {
				t.Errorf("%d hand-backs, want %d:\n%s", n, 1+len(tc.more), ev)
			}
			moved := "A's worker finished the rebase, but main moved on meanwhile: rebasing wt/A onto it before checking it"
			if got := strings.Contains(ev, moved); got != (tc.lands != nil) {
				t.Errorf("%q in events is %v, want %v:\n%s", moved, got, tc.lands != nil, ev)
			}
			if got := read(t, filepath.Join(h.repo, "shared.txt")); got != tc.wantShared {
				t.Errorf("shared.txt on main = %q", got)
			}
			if tc.lands != nil && !strings.Contains(h.mainLog(), "landed on main: "+tc.lands[0]) {
				t.Errorf("history:\n%s", h.mainLog())
			}
		})
	}
}

// A fix committed while another ticket merges onto Base is checked once, on the branch rebased onto
// it, and merges.
func TestAFixIsCheckedOnceOnTheBaseItMergesOnto(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		h := checkHandBackHarness(t, dir, func(w *fakeWorker) AgentState {
			w.git.commit("main", "landed on main: b.txt", "b.txt")
			return commitsFix(dir, "admin_test.go", true)(w)
		})
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"FIXING: '" + h.cfg.Check + "' fails on wt/A rebased onto main",
			"A's worker committed 1 commit, but main moved on meanwhile: rebasing wt/A onto it before checking it",
			"rebased wt/A onto main, which moved on while it ran",
			"'" + h.cfg.Check + "' passes on wt/A as its worker fixed it",
			"A closed",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		if n := checkRuns(t, dir, "A"); n != 2 {
			t.Errorf("A's check ran %d times, want 2: once failing, once after its fix:\n%s", n, ev)
		}
		log := h.mainLog()
		for _, want := range []string{"landed on main: b.txt", "A: add a.txt", "A: fix admin_test.go"} {
			if !strings.Contains(log, want) {
				t.Errorf("history lacks %q:\n%s", want, log)
			}
		}
	})
}
