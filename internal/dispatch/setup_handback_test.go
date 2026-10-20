package dispatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// A worker handed back its ticket after a rebase that brought in a dependency change works on
// dependencies set up for the rebased code: orchestra-nr1v.7, from tutti's runs of 2026-10-18 to 10-20,
// where workers resolving conflicts ran their tests on node_modules installed before main added a
// dependency ("Cannot find module '@opentui/keymap'").

// resolveSetupHarness is setupHarness's run, online, with shared.txt for tickets to conflict on and
// conflicts handed back.
func resolveSetupHarness(t *testing.T, dir string) *harness {
	t.Helper()
	h := setupHarness(t, dir)
	online(t, dir)
	if err := os.WriteFile(filepath.Join(h.repo, "shared.txt"), []byte("line 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.git(h.repo, "add", ".")
	h.git(h.repo, "commit", "-q", "-m", "shared file")
	h.cfg.Check = "! grep -q '<<<<<<<' shared.txt && cmp package-lock.json deps.lock"
	h.cfg.ResolveConflicts = true
	h.cfg.ResolveTimeout = patience
	return h
}

// A rebase that stops on conflicts and brings in a lockfile change is handed back with the setup named,
// to run once the rebase has finished and before the tests; one that brings in no change to a file the
// setup reads is handed back without it.
func TestAConflictHandBackNamesTheSetupWhenTheRebaseChangesALockfile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, file string
		named      bool
	}{
		{"lockfile changed", "package-lock.json", true},
		{"no dependency file changed", "README.md", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			h := resolveSetupHarness(t, dir)
			h.beads.add("A", "first", 1)
			h.worker("A", func(w *fakeWorker) AgentState {
				installs(w)
				landsOnMain(w, h.repo, tc.file, "v2\n")
				return conflicting(h.repo)(w)
			}, resolvesWith("main\nA\n"))
			o, code := h.run()
			if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
				t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			if got := closedIDs(h); !equal(got, []string{"A"}) {
				t.Errorf("merged %v, want A\n%s", got, h.sink.text())
			}
			pasted := h.herdr.pastedText("A")
			if len(pasted) != 1 {
				t.Fatalf("pasted to A %q, want the hand-back", pasted)
			}
			p := pasted[0]
			told := "As the rebase brings in a change to package-lock.json, which the project's setup reads, once " +
				"the rebase has finished run the setup, '" + h.cfg.Setup + "', in the foreground before the tests"
			if strings.Contains(p, told) != tc.named {
				t.Errorf("the hand-back should say %q only when the rebase changes the lockfile:\n%s", told, p)
			}
			if i, j := strings.Index(p, "rebase --continue"), strings.Index(p, "run the setup"); tc.named &&
				(i < 0 || j < i) {
				t.Errorf("the setup should come after the rebase continues:\n%s", p)
			}
			line := "RESOLVING: wt/A conflicts with main in shared.txt; handed back to its worker in tab tab1 to " +
				"resolve (up to " + command.ShortDuration(patience) + "); told to run '" + h.cfg.Setup + "' once the rebase has finished, as the rebase " +
				"brings in a change to package-lock.json, which the project's setup reads\n"
			if ev := h.sink.text(); strings.Contains(ev, line) != tc.named {
				t.Errorf("events should say %q only when the rebase changes the lockfile:\n%s", line, ev)
			}
		})
	}
}

// A check that fails on a branch rebased over a lockfile change is handed back to its worker to fix only
// after the setup has run in its worktree, so that the worker works on the dependencies the check had.
func TestAFailedCheckIsHandedBackAfterTheSetupRuns(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := setupHarness(t, dir)
	online(t, dir)
	h.cfg.Check = "cmp package-lock.json deps.lock && test -f '" + dir + "'/fixed"
	h.cfg.CheckHandBacks = 1
	h.cfg.ResolveTimeout = patience
	h.beads.add("A", "first", 1)
	runs := -1
	h.worker("A", bumpedMeanwhile(h.repo, "package-lock.json"), func(w *fakeWorker) AgentState {
		runs = setupRuns(t, dir)
		return commitsFix(dir, "fix.txt", true)(w)
	})
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := closedIDs(h); !equal(got, []string{"A"}) {
		t.Errorf("merged %v, want A\n%s", got, h.sink.text())
	}
	if runs != 2 {
		t.Errorf("the setup had run %d times when the failure was handed back, want twice: in its new worktree, "+
			"and before the check\n%s", runs, h.sink.text())
	}
	ev := h.sink.text()
	for _, want := range []string{
		"A: package-lock.json changed since its dependencies were last set up; running '",
		"FIXING: '" + h.cfg.Check + "' fails on wt/A rebased onto main; handed back to its worker",
		"passes on wt/A as its worker fixed it",
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("events lack %q:\n%s", want, ev)
		}
	}
}
