package dispatch

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// The full check (check_full) runs once after a run that ended by itself and merged a ticket, on
// Base's head in a worktree of its own; a failure files one ticket for its suite.

// failingFullCheck fails in the suite lint, after the suite unit tests passed.
const failingFullCheck = `echo 'SUITE: unit tests'; echo 'ok  ./internal/a'; echo 'SUITE: lint'; ` +
	`echo 'internal/a/a.go:3:1: exported A has no comment'; echo '1 issues:'; exit 1`

// runAndCheck runs the loop as orchestra does, then the full check when it is due, and returns the
// loop and the run's exit code.
func (h *harness) runAndCheck(ctx context.Context) (*Loop, int) {
	o := h.loop()
	code := o.Run(ctx)
	if o.FullCheckDue(code) {
		o.FullCheck(context.WithoutCancel(ctx))
	}
	return o, code
}

// fullChecks is the full check's events, as "<detail> <text>".
func (s *runSink) fullChecks() []string {
	var l []string
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ev := range s.events {
		if ev.Kind == EvFullCheck {
			l = append(l, ev.Detail+" "+strings.TrimSpace(ev.Text))
		}
	}
	return l
}

// filedForFullCheck is the tickets labelled FullCheckLabel.
func filedForFullCheck(h *harness) []Ticket {
	open, _ := h.beads.Unclosed(context.Background())
	var filed []Ticket
	for _, t := range open {
		if HasLabel(t, FullCheckLabel) {
			filed = append(filed, t)
		}
	}
	return filed
}

func TestFullCheckRunsAfterARunThatMerged(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		ran := filepath.Join(t.TempDir(), "ran")
		h.cfg.CheckFull = "pwd > " + ran + "; echo 'SUITE: unit tests'; echo 'FLAKY: ./a TestSlow'"
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		o, code := h.runAndCheck(t.Context())
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		got := h.sink.fullChecks()
		if len(got) != 1 || !strings.HasPrefix(got[0], "passed FULL_CHECK passed: '"+h.cfg.CheckFull+"' on main at ") {
			t.Errorf("full check events %q\n%s", got, h.sink.text())
		}
		wt := strings.TrimSpace(read(t, ran))
		if filepath.Dir(wt) != h.cfg.WTRoot || !strings.HasPrefix(filepath.Base(wt), "check-full-") {
			t.Errorf("ran in %s, want a worktree of its own in %s", wt, h.cfg.WTRoot)
		}
		if exists(wt) {
			t.Errorf("its worktree %s is left", wt)
		}
		if !strings.Contains(h.sink.text(),
			"FLAKY: the full check '"+h.cfg.CheckFull+"' passed, but a test failed and then passed on a rerun: ./a TestSlow") {
			t.Errorf("its FLAKY: line should be a warning:\n%s", h.sink.text())
		}
		if f, ok := o.fullCheckOf(); !ok || f.outcome != FullCheckPassed || f.report(h.cfg.CheckFull) != "" {
			t.Errorf("for the reviewer: %+v, %v", f, ok)
		}
	})
}

func TestFullCheckDoesNotRunAfterARunThatMergedNothing(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.CheckFull = "exit 1"
		h.beads.add("A", "first", 1)
		h.worker("A", func(w *fakeWorker) AgentState { w.claim(); w.deferIt(); return "idle" })
		if _, code := h.runAndCheck(t.Context()); code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if got := h.sink.fullChecks(); len(got) > 0 {
			t.Errorf("the full check ran: %q", got)
		}
	})
}

func TestFullCheckDoesNotRunAfterAHold(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.CheckFull = "exit 1"
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", finishes("a.txt"))
		h.worker("B", func(w *fakeWorker) AgentState { w.claim(); return "idle" }) // PAUSED
		if _, code := h.runAndCheck(t.Context()); code != ExitStuck {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if !strings.Contains(h.mainLog(), "A: add a.txt") {
			t.Errorf("A should have merged:\n%s", h.mainLog())
		}
		if got := h.sink.fullChecks(); len(got) > 0 {
			t.Errorf("the full check ran: %q", got)
		}
	})
}

func TestFullCheckDoesNotRunAfterCtrlC(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.CheckFull = "exit 1"
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		h.beads.add("A", "first", 1)
		h.beads.add("B", "second", 2)
		h.worker("A", finishes("a.txt"))
		h.worker("B", func(w *fakeWorker) AgentState { cancel(); return finishes("b.txt")(w) })
		if _, code := h.runAndCheck(ctx); code != ExitInterrupted {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if got := h.sink.fullChecks(); len(got) > 0 {
			t.Errorf("the full check ran: %q", got)
		}
	})
}

// A run without check_full (a project not set up again since it had one check) has no full check.
func TestFullCheckDoesNotRunWithoutOne(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		if o, code := h.runAndCheck(t.Context()); code != ExitOK || o.FullCheckDue(code) {
			t.Fatalf("exit %d, due %v", code, o.FullCheckDue(code))
		}
	})
}

// A failure keeps the whole output in the main checkout's .orchestra/run/check-full.log and files
// one ticket for the suite it failed in; the next failure in that suite, while it is open, files
// none but notes it there.
func TestFullCheckFailureFilesOneTicketForItsSuite(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.CheckFull = failingFullCheck
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		o, code := h.runAndCheck(t.Context())
		if code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		saved := filepath.Join(h.repo, project.RunPath(fullCheckLogName))
		if got := read(t, saved); !strings.HasPrefix(got, "SUITE: unit tests\nok  ./internal/a\n") ||
			!strings.HasSuffix(got, "1 issues:\n") {
			t.Errorf("%s holds:\n%s", saved, got)
		}
		filed := filedForFullCheck(h)
		if len(filed) != 1 || filed[0].Title != "Full check fails: lint" || filed[0].IssueType != "bug" ||
			*filed[0].Priority != 2 {
			t.Fatalf("filed %+v", filed)
		}
		bug := filed[0]
		if !strings.Contains(bug.Description, "in the suite lint") || !strings.Contains(bug.Description, saved) ||
			!strings.Contains(bug.Description, "internal/a/a.go:3:1: exported A has no comment") {
			t.Errorf("its description:\n%s", bug.Description)
		}
		var ev Event
		for _, e := range h.sink.events {
			if e.Kind == EvFullCheck {
				ev = e
			}
		}
		if ev.Detail != FullCheckFailed || ev.Suite != "lint" || ev.Output != saved || ev.Ticket != bug.ID ||
			!strings.Contains(ev.Text, "FULL_CHECK_FAILED: '"+h.cfg.CheckFull+"' fails on main at ") ||
			!strings.HasSuffix(ev.Text, "; filed "+bug.ID) {
			t.Errorf("its event: %+v", ev)
		}
		if !h.alerts.has("Full check failed · lint") {
			t.Errorf("notifications: %q", h.alerts.list())
		}
		if log := h.logged(); !strings.Contains(log, "internal/a/a.go:3:1: exported A has no comment\n1 issues:") {
			t.Errorf("the log should keep the end of the output:\n%s", log)
		}
		f, _ := o.fullCheckOf()
		if r := f.report(h.cfg.CheckFull); !strings.Contains(r, "## Full check") || !strings.Contains(r, bug.ID) ||
			!strings.Contains(r, "1 issues:") {
			t.Errorf("the report says:\n%s", r)
		}
		if e := f.evidence(h.cfg.CheckFull, "main"); !strings.Contains(e, "failed on main") ||
			!strings.Contains(e, "in the suite lint") || !strings.Contains(e, "exported A has no comment") {
			t.Errorf("the reviewer is told:\n%s", e)
		}

		// Failing again in lint, as the next run's check would, while bug is open.
		o.FullCheck(t.Context())
		if again := filedForFullCheck(h); len(again) != 1 {
			t.Errorf("filed a second ticket: %+v", again)
		}
		if notes := h.beads.notesOf(bug.ID); !strings.Contains(notes, "fails again on main at ") {
			t.Errorf("%s's notes: %q", bug.ID, notes)
		}
		if got := h.sink.fullChecks(); len(got) != 2 || !strings.HasSuffix(got[1], bug.ID+" is open for it already") {
			t.Errorf("full check events %q", got)
		}
	})
}

// A check that says nothing of its suites is named for the check itself.
func TestFullCheckWithoutSuiteLinesIsNamedForItself(t *testing.T) {
	if got := failingSuite([]byte("ok\nFAIL\n")); got != "" {
		t.Errorf("suite %q", got)
	}
	if got := failingSuite([]byte("SUITE: a\n  SUITE:  go  test \nSUITE:\nboom\n")); got != "go test" {
		t.Errorf("suite %q", got)
	}
	if got := fullCheckTitle(strings.Repeat("x", 80)); len([]rune(got)) != titleWidth || !strings.HasSuffix(got, "…") {
		t.Errorf("title %q", got)
	}
}

// The full check runs on main's head in a worktree of its own: whatever it does there, the main
// checkout is unchanged, and the worktree is gone afterwards, git's record of it too.
func TestFullCheckLeavesTheMainCheckoutAsItIs(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	head := filepath.Join(t.TempDir(), "head")
	h.cfg.CheckFull = "git rev-parse HEAD > " + head + "; touch made-by-check; echo junk >> a.txt; " + failingFullCheck
	h.beads.add("A", "first", 1)
	h.worker("A", finishes("a.txt"))
	if _, code := h.runAndCheck(t.Context()); code != ExitOK {
		t.Fatalf("exit %d\n%s", code, h.sink.text())
	}
	if got, want := strings.TrimSpace(read(t, head)), strings.TrimSpace(h.git(h.repo, "rev-parse", "main")); got != want {
		t.Errorf("checked %s, want main's head %s", got, want)
	}
	if st := h.git(h.repo, "status", "--porcelain"); st != "" {
		t.Errorf("the main checkout changed: %q", st)
	}
	if exists(filepath.Join(h.repo, "made-by-check")) {
		t.Error("the check ran in the main checkout")
	}
	if wts := h.git(h.repo, "worktree", "list", "--porcelain"); strings.Count(wts, "worktree ") != 1 {
		t.Errorf("worktrees left:\n%s", wts)
	}
	entries, err := os.ReadDir(h.cfg.WTRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "check-full-") {
			t.Errorf("%s is left in %s", e.Name(), h.cfg.WTRoot)
		}
	}
	if len(filedForFullCheck(h)) != 1 {
		t.Errorf("filed %+v", filedForFullCheck(h))
	}
}

// Ctrl+C while the full check runs stops it, and everything it started, and removes its worktree;
// nothing is filed.
func TestFullCheckStopsWithCtrlC(t *testing.T) {
	t.Parallel()
	h := newTimedHarness(t) // on the real clock: the check is a real process
	started := filepath.Join(t.TempDir(), "started")
	h.cfg.CheckFull = "touch " + started + "; echo 'SUITE: e2e'; sleep 60"
	o := h.loop()
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		for !exists(started) {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		o.FullCheck(ctx)
	}()
	select {
	case <-done:
	case <-time.After(patience):
		t.Fatal("the full check went on after Ctrl+C")
	}
	if got := h.sink.fullChecks(); len(got) != 1 || !strings.HasPrefix(got[0], "skipped FULL_CHECK_SKIPPED: ") {
		t.Errorf("full check events %q", got)
	}
	if filed := filedForFullCheck(h); len(filed) > 0 {
		t.Errorf("filed %+v", filed)
	}
	if entries, _ := os.ReadDir(h.cfg.WTRoot); len(entries) > 0 {
		t.Errorf("left in %s: %v", h.cfg.WTRoot, entries)
	}
}

// The reviewer is told how the full check went, with the end of a failure's output.
func TestFullCheckIsInTheReviewersEvidence(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.CheckFull = failingFullCheck
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		o, _ := h.runAndCheck(t.Context())
		in := o.reviewInput(t.Context(), ExitOK, o.Final())
		if !strings.Contains(in, "The full check, run once on main after the run's last merge") ||
			!strings.Contains(in, "in the suite lint") || !strings.Contains(in, "exported A has no comment") {
			t.Errorf("the reviewer's evidence:\n%s", in)
		}
	})
}
