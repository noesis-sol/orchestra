package dispatch

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

// The setup command runs before the check on a rebased branch whose rebase changed a dependency
// file (see setUp): orchestra-ikmn, from the ancient-chat run of 2026-10-05, where two tickets were
// set aside as their checks ran against node_modules installed before a merged ticket bumped a
// dependency.

// setupHarness is a run of a repository with a lockfile, package-lock.json, whose installed
// dependencies are deps.lock, a copy of it that git ignores. The setup installs them, noting each
// run in dir, and fails, printing "npm ERR! offline", until dir holds the file online; the check
// fails unless they are installed for the lockfile.
func setupHarness(t *testing.T, dir string) *harness {
	t.Helper()
	h := newHarness(t)
	for file, content := range map[string]string{".gitignore": ".orchestra/\ndeps.lock\n", "package-lock.json": "v1\n"} {
		if err := os.WriteFile(filepath.Join(h.repo, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.git(h.repo, "add", ".")
	h.git(h.repo, "commit", "-q", "-m", "lockfile")
	h.cfg.Setup = fmt.Sprintf(`echo run >> '%s'/setup.runs; if test -f '%s'/online; `+
		`then cp package-lock.json deps.lock; else echo 'npm ERR! offline'; exit 1; fi`, dir, dir)
	h.cfg.SetupFiles = project.DefaultSetupFiles
	h.cfg.Check = "cmp package-lock.json deps.lock"
	return h
}

// online lets setupHarness's setup succeed.
func online(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "online"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

// setupRuns is how many times setupHarness's setup ran.
func setupRuns(t *testing.T, dir string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "setup.runs"))
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(b), "run\n")
}

// installs sets up the worker's dependencies as it starts, as a worker running npm ci does.
func installs(w *fakeWorker) {
	b, err := os.ReadFile(filepath.Join(w.wt, "package-lock.json"))
	if err != nil {
		w.t.Error(err)
	}
	w.write(w.wt, "deps.lock", string(b))
}

// bumpedMeanwhile claims the ticket and installs its dependencies, has main change file while it
// works, commits its own change and closes it.
func bumpedMeanwhile(repo, file string) behaviour {
	return func(w *fakeWorker) AgentState {
		w.claim()
		installs(w)
		landsOnMain(w, repo, file, "v2\n")
		w.commit(w.id + ".txt")
		w.close()
		return "idle"
	}
}

// A ticket rebased over a lockfile change has its dependencies set up again before its check, and
// merges; one rebased over other changes doesn't.
func TestTheSetupRunsBeforeTheCheckWhenTheRebaseChangesALockfile(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, file string
		runs       int
	}{
		{"lockfile changed", "package-lock.json", 1},
		{"no dependency file changed", "README.md", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			h := setupHarness(t, dir)
			online(t, dir)
			h.beads.add("A", "first", 1)
			h.worker("A", bumpedMeanwhile(h.repo, tc.file))
			o, code := h.run()
			if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
				t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
			}
			if got := closedIDs(h); !equal(got, []string{"A"}) {
				t.Errorf("merged %v, want A\n%s", got, h.sink.text())
			}
			if n := setupRuns(t, dir); n != tc.runs {
				t.Errorf("the setup ran %d times, want %d\n%s", n, tc.runs, h.sink.text())
			}
			ev := h.sink.text()
			said := "A: package-lock.json changed in the rebase; running '" + h.cfg.Setup + "' before the check\n"
			if strings.Contains(ev, said) != (tc.runs > 0) {
				t.Errorf("events should say %q only when the setup runs:\n%s", said, ev)
			}
			if want := "'cmp package-lock.json deps.lock' passes on the rebased wt/A"; !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		})
	}
}

// A setup that fails sets the ticket aside as a failing check does, its output kept beside the
// check's. Checked again once Base moves on, the ticket has its setup run again, though that
// rebase changes no dependency file: the dependencies are still as they were before the lockfile
// changed.
func TestAFailedSetupSetsTheTicketAsideUntilItsRecheck(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := setupHarness(t, dir)
	h.beads.add("A", "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("A", bumpedMeanwhile(h.repo, "package-lock.json"))
	h.worker("B", func(w *fakeWorker) AgentState {
		if !unmergedLabel(t, h, "A") {
			t.Error("A should be labelled unmerged while it is set aside")
		}
		// Not read: its t.Fatal would leave the worker unsettled.
		if b, err := os.ReadFile(filepath.Join(h.worktree("A"), ".orchestra", "run", setupLogName)); string(b) != "npm ERR! offline\n" {
			t.Errorf("A's setup.log = %q, %v", b, err)
		}
		online(t, dir)
		return finishes("b.txt")(w)
	})
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 2 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := closedIDs(h); !equal(got, []string{"B", "A"}) {
		t.Errorf("merged %v, want B, then A checked again\n%s", got, h.sink.text())
	}
	if n := setupRuns(t, dir); n != 2 {
		t.Errorf("the setup ran %d times, want 2: as A merged and as it was checked again\n%s", n, h.sink.text())
	}
	ev := h.sink.text()
	for _, want := range []string{
		"CHECKS_FAILED: A closed, but the setup '" + h.cfg.Setup + "', run before the check, fails on wt/A " +
			"rebased onto main; ",
		filepath.Join(".orchestra", "run", setupLogName) + "); checked once more if main moves on in this run\n",
		"RECHECK: A's check failed on main at ",
		"A: package-lock.json changed in the rebase; running '",
		"'cmp package-lock.json deps.lock' passes on the rebased wt/A",
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("events lack %q:\n%s", want, ev)
		}
	}
	if aside := eventsOf(h, EvWarn); len(aside) != 1 || aside[0].Detail != "setup failed" {
		t.Errorf("warnings %+v, want A's setup failed", aside)
	}
}

// A rebase that stops on conflicts and brings in a lockfile change has the dependencies set up again
// before orchestra checks its worker's resolution.
func TestTheSetupRunsBeforeAResolvedRebaseIsChecked(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
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
	h.beads.add("A", "first", 1)
	h.worker("A", func(w *fakeWorker) AgentState {
		installs(w)
		landsOnMain(w, h.repo, "package-lock.json", "v2\n")
		return conflicting(h.repo)(w)
	}, resolvesWith("main\nA\n"))
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if got := closedIDs(h); !equal(got, []string{"A"}) {
		t.Errorf("merged %v, want A\n%s", got, h.sink.text())
	}
	if n := setupRuns(t, dir); n != 1 {
		t.Errorf("the setup ran %d times, want once, before the resolution was checked\n%s", n, h.sink.text())
	}
	ev := h.sink.text()
	for _, want := range []string{
		"RESOLVING: wt/A conflicts with main in shared.txt",
		"A: package-lock.json changed in the rebase; running '",
		"passes on wt/A as its worker resolved it",
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("events lack %q:\n%s", want, ev)
		}
	}
}

// A resolution whose setup fails is set aside as one whose check fails.
func TestAResolvedRebaseWhoseSetupFailsIsSetAside(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	h := setupHarness(t, dir)
	if err := os.WriteFile(filepath.Join(h.repo, "shared.txt"), []byte("line 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.git(h.repo, "add", ".")
	h.git(h.repo, "commit", "-q", "-m", "shared file")
	h.cfg.ResolveConflicts = true
	h.cfg.ResolveTimeout = patience
	h.beads.add("A", "first", 1)
	h.worker("A", func(w *fakeWorker) AgentState {
		installs(w)
		landsOnMain(w, h.repo, "package-lock.json", "v2\n")
		return conflicting(h.repo)(w)
	}, resolvesWith("main\nA\n"))
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	why := "handed back to its worker, but the setup '" + h.cfg.Setup + "', run before the check, fails on the " +
		"resolved wt/A (output is in "
	if ev := h.sink.text(); !strings.Contains(ev, "MERGE_CONFLICT: A closed") || !strings.Contains(ev, why) {
		t.Errorf("events lack %q:\n%s", why, ev)
	}
	if !unmergedLabel(t, h, "A") {
		t.Error("A should be labelled unmerged")
	}
}

func TestSetupFilesIn(t *testing.T) {
	t.Parallel()
	files := []string{"package-lock.json", "web/package-lock.json", "go.sum", "requirements-dev.txt", "src/a.ts",
		"web/yarn.lock"}
	for _, tc := range []struct {
		globs []string
		want  string
	}{
		{project.DefaultSetupFiles, "package-lock.json web/package-lock.json go.sum requirements-dev.txt web/yarn.lock"},
		{[]string{"/package-lock.json"}, "package-lock.json"},
		{[]string{"web/*.lock"}, "web/yarn.lock"},
		{[]string{"*.ts"}, "src/a.ts"},
		{[]string{"["}, ""}, // a bad glob, which settings reject, matches nothing
		{nil, ""},
	} {
		if got := strings.Join(setupFilesIn(files, tc.globs), " "); got != tc.want {
			t.Errorf("setupFilesIn(%q) = %q, want %q", tc.globs, got, tc.want)
		}
	}
	if got := fileList(strings.Fields("a b c d e f g")); got != "a, b, c, d, e and 2 more" {
		t.Errorf("fileList = %q", got)
	}
}

// The reviewer is told a setup failed before the check could run, and a ticket whose setup failed is
// checked again whatever its output names.
func TestASetupFailureForTheReviewer(t *testing.T) {
	t.Parallel()
	f := checkFail{br: "wt/A", onto: "0123456789abcdef", how: "fails", output: "setup.log", setup: "npm ci",
		said: []string{"internal/a/a.go:3:1: x"}, dirs: []string{"internal/a"}}
	want := "A: the setup 'npm ci', run before the check as the rebase changed the project's dependency files, " +
		"fails on wt/A rebased onto main at 0123456789, so the check did not run; its output is in setup.log.\n"
	if ev := f.evidence("A", "make check", "main"); !strings.Contains(ev, want) {
		t.Errorf("the evidence lacks %q:\n%s", want, ev)
	}
	if own := f.ownFailures(); len(own) != 0 {
		t.Errorf("ownFailures = %v, want none for a failed setup", own)
	}
}
