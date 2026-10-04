package dispatch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// What orchestra keeps of the merge check's output: the whole of a failing check's, and the FLAKY:
// lines of a passing one.

// landsOnMainMeanwhile claims the ticket, has main move on while it works, so its branch is
// rebased and checked before it merges, then commits file and closes it.
func landsOnMainMeanwhile(repo, file string) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		landsOnMain(w, repo, "landed.txt", "landed\n")
		w.commit(file)
		w.close()
		return "idle"
	}
}

// orchestra-4wb.3 on 2026-10-04: go test prints the failing test's name and error first, and a long
// failure message after them; the log keeps the last 40 lines, which left only the end of a screen
// dump and 'FAIL cmd/orchestra'. The whole output is kept in the worktree, which CHECKS_FAILED names.
func TestAFailingChecksWholeOutputIsKeptInItsWorktree(t *testing.T) {
	t.Parallel()
	var want strings.Builder
	want.WriteString("--- FAIL: TestFirst (0.01s)\n    first_test.go:12: got 1, want 2\n")
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&want, "screen line %d\n", i)
	}
	want.WriteString("FAIL cmd/orchestra\n")
	output := filepath.Join(t.TempDir(), "output")
	if err := os.WriteFile(output, []byte(want.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newHarness(t)
	h.cfg.Check = "cat " + output + "; exit 1"
	h.beads.add("A", "first", 1)
	h.worker("A", landsOnMainMeanwhile(h.repo, "a.txt"))
	if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}

	saved := filepath.Join(h.worktree("A"), project.RunPath(checkLogName))
	if got := read(t, saved); got != want.String() {
		t.Errorf("%s holds:\n%s\nwant:\n%s", saved, got, want.String())
	}
	if ev := h.sink.text(); !strings.Contains(ev, "CHECKS_FAILED: A closed, but '"+h.cfg.Check+"' fails on wt/A") ||
		!strings.Contains(ev, "left for review (output is in "+saved+")") {
		t.Errorf("CHECKS_FAILED should name %s; events:\n%s", saved, ev)
	}
	log := h.logged()
	if !strings.Contains(log, "screen line 100\nFAIL cmd/orchestra") {
		t.Errorf("the log should keep the end of the output:\n%s", log)
	}
	if strings.Contains(log, "--- FAIL: TestFirst") {
		t.Errorf("the log should keep only the end of the output, the file the whole of it:\n%s", log)
	}
	if st := h.git(h.worktree("A"), "status", "--porcelain"); st != "" {
		t.Errorf("the saved output should not show in the worktree's git status: %q", st)
	}
}

// A check that reruns a failed test and passes rescues a flaky test: the ticket merges, and each
// FLAKY: line the check printed is a warning naming the ticket.
func TestAPassingChecksFlakyLinesAreWarnings(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.Check = `echo 'ok  ./internal/a'; echo 'FLAKY: ./internal/dispatch TestSlow'; ` +
		`echo '    FLAKY: a test logging this is no FLAKY line'; echo 'FLAKY:'; echo 'FLAKY: ./cmd/orchestra TestRun'`
	h.beads.add("A", "first", 1)
	h.worker("A", landsOnMainMeanwhile(h.repo, "a.txt"))
	if o, code := h.run(); code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if log := h.mainLog(); !strings.Contains(log, "A: add a.txt") {
		t.Errorf("A should merge as a passing check's ticket does; main:\n%s", log)
	}
	said := "A   FLAKY: A's check '" + h.cfg.Check + "' passed, but a test failed and then passed on a rerun: "
	want := []string{said + "./internal/dispatch TestSlow", said + "it doesn't say which", said + "./cmd/orchestra TestRun"}
	if got := warnings(h, "A"); !equal(got, want) {
		t.Errorf("warnings:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, ev := range h.sink.events {
		if ev.Kind == EvWarn && ev.Aside {
			t.Errorf("a FLAKY warning should not set A aside: %+v", ev)
		}
	}
	if log := h.logged(); !strings.Contains(log, "FLAKY: A's check") {
		t.Errorf("the log should have the warnings:\n%s", log)
	}
}

// The output of a check that fails in a worktree whose .orchestra is a symlink is not written where
// the link points: CHECKS_FAILED names the log, which has the end of it.
func TestAFailingChecksOutputIsNotWrittenThroughASymlink(t *testing.T) {
	f := newMergeFixture(t, "echo 'checked'; exit 1")
	wt := f.ticket(t, "k-1", "a.txt", "a\n")
	f.onMain(t, "b.txt", "b\n")
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(wt, project.Dir)); err != nil {
		t.Fatal(err)
	}
	if s := f.orch.merge(context.Background(), "k-1", "wt/k-1", wt, "tab"); s != nil {
		t.Fatal(s)
	}
	if ev := f.sink.text(); !strings.Contains(ev, "CHECKS_FAILED: k-1") || !strings.Contains(ev, "(output is in log)") {
		t.Errorf("CHECKS_FAILED should name the log; events:\n%s", ev)
	}
	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 {
		t.Errorf("written where the symlink points: %v (%v)", entries, err)
	}
}
