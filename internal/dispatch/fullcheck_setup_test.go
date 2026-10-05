package dispatch

import (
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// The full check's worktree is new, with nothing installed in it, so the project's setup runs there
// first, within the full check's time limit (orchestra-aqd3); a setup that fails files no ticket.

// The setup runs in the full check's worktree before check_full, which finds what it installed.
func TestFullCheckRunsTheSetupFirst(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		dir := t.TempDir()
		h.cfg.Setup = "pwd > " + filepath.Join(dir, "setup") + "; touch installed"
		h.cfg.CheckFull = "test -f installed && pwd > " + filepath.Join(dir, "check")
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		if _, code := h.runAndCheck(t.Context()); code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if got := h.sink.fullChecks(); len(got) != 1 || !strings.HasPrefix(got[0], "passed FULL_CHECK passed: ") {
			t.Fatalf("full check events %q\n%s", got, h.sink.text())
		}
		setup, check := read(t, filepath.Join(dir, "setup")), read(t, filepath.Join(dir, "check"))
		if setup != check || !strings.HasPrefix(filepath.Base(strings.TrimSpace(setup)), "check-full-") {
			t.Errorf("the setup ran in %s, the check in %s", setup, check)
		}
		want := "FULL_CHECK: running '" + h.cfg.Setup + "', then '" + h.cfg.CheckFull + "', on main at "
		if !strings.Contains(h.sink.text(), want) {
			t.Errorf("want %q in:\n%s", want, h.sink.text())
		}
	})
}

// A setup that fails stops the full check there: its output is kept as a failed check's is, the
// line, the notification, the reviewer and the report say the setup failed, and no ticket is filed.
func TestFullCheckSetupFailureFilesNoTicket(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		ran := filepath.Join(t.TempDir(), "ran")
		h.cfg.Setup = "echo 'SUITE: e2e'; echo 'npm ERR! offline'; exit 1"
		h.cfg.CheckFull = "touch " + ran
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		o, code := h.runAndCheck(t.Context())
		if code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if exists(ran) {
			t.Error("check_full ran after its setup failed")
		}
		saved := filepath.Join(h.repo, project.RunPath(fullCheckLogName))
		if got := read(t, saved); got != "SUITE: e2e\nnpm ERR! offline\n" {
			t.Errorf("%s holds:\n%s", saved, got)
		}
		if filed := filedForFullCheck(h); len(filed) > 0 {
			t.Errorf("filed %+v", filed)
		}
		var ev Event
		for _, e := range h.sink.events {
			if e.Kind == EvFullCheck {
				ev = e
			}
		}
		want := "FULL_CHECK_FAILED: the setup '" + h.cfg.Setup + "', run before '" + h.cfg.CheckFull + "', fails on main at "
		if ev.Detail != FullCheckFailed || ev.Suite != "" || ev.Ticket != "" || ev.Output != saved ||
			!strings.Contains(ev.Text, want) || !strings.HasSuffix(ev.Text, "; no ticket filed: it is not a suite's failure") {
			t.Errorf("its event: %+v", ev)
		}
		if !h.alerts.has("Full check failed · setup") {
			t.Errorf("notifications: %q", h.alerts.list())
		}
		if log := h.logged(); !strings.Contains(log, "npm ERR! offline") {
			t.Errorf("the log should keep the end of the output:\n%s", log)
		}
		f, _ := o.fullCheckOf()
		if r := f.report(h.cfg.CheckFull); !strings.Contains(r, "the setup before it, `"+h.cfg.Setup+"`, failed") ||
			!strings.Contains(r, "npm ERR! offline") {
			t.Errorf("the report says:\n%s", r)
		}
		if e := f.evidence(h.cfg.CheckFull, "main"); !strings.Contains(e, "did not run: the setup '"+h.cfg.Setup) ||
			!strings.Contains(e, "failed on main") || !strings.Contains(e, "npm ERR! offline") {
			t.Errorf("the reviewer is told:\n%s", e)
		}
	})
}

// The setup and check_full share the full check's time limit: a check that would finish within it
// alone is stopped once the setup has used most of it.
func TestFullCheckSetupCountsInItsTimeLimit(t *testing.T) {
	t.Parallel()
	h := newTimedHarness(t) // on the real clock: the setup and the check are real processes
	ran := filepath.Join(t.TempDir(), "ran")
	h.cfg.CheckFullTimeout = 300 * time.Millisecond
	h.cfg.Setup = "sleep 0.2"
	h.cfg.CheckFull = "sleep 0.2; touch " + ran
	h.loop().FullCheck(t.Context())
	if got := h.sink.fullChecks(); len(got) != 1 || !strings.HasPrefix(got[0], FullCheckTimedOut+" ") ||
		!strings.Contains(got[0], "did not finish within 300ms") {
		t.Errorf("full check events %q", got)
	}
	if exists(ran) {
		t.Error("check_full ran to its end past the time limit")
	}
}
