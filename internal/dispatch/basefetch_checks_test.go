package dispatch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/project"
)

// The check commands a start's fast-forward of Base brings in are the run's (orchestra-5zii): no ticket
// has started, so a commit upstream that renames the merge check doesn't leave the run checking every
// ticket with the old command on the new head.

// A fast-forward to a commit that changes check_fast, check_full and check_own has the run merge its
// first ticket with the new merge check, keep the new full check and worker's check for the rest of
// it, and say so as it starts.
func TestTheStartsFastForwardBringsInTheChecks(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	h.cfg.Check, h.cfg.CheckOwn = "false", "" // the old merge check would fail A's merge
	other := withRemote(h)
	ran := filepath.Join(t.TempDir(), "ran")
	fast := "basename \"$PWD\" >> " + command.ShellQuote(ran)
	settings, err := json.Marshal(project.Settings{CheckFast: fast, CheckFull: "true", CheckOwn: "scripts/own.sh"})
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(other, project.SettingsFile)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, settings, 0o644); err != nil {
		t.Fatal(err)
	}
	h.git(other, "add", "-f", project.SettingsFile) // the harness's repository ignores .orchestra/
	h.git(other, "commit", "-q", "-m", "Rename the checks")
	h.git(other, "push", "-q", "origin", "main")

	h.beads.add("A", "checked by the new merge check", 1)
	h.worker("A", finishes("a.txt"))
	o := runFetching(h)
	if got := setUpIn(t, ran); len(got) != 1 {
		t.Errorf("the new merge check ran in %v, want once, on A's branch", got)
	}
	if got := closedIDs(h); !equal(got, []string{"A"}) {
		t.Errorf("merged %v, want A, with the new merge check\n%s", got, h.sink.text())
	}
	if c := o.cfg; c.Check != fast || c.CheckFull != "true" || c.CheckOwn != "scripts/own.sh" {
		t.Errorf("checks %q, %q, %q: want those the fast-forward brought in", c.Check, c.CheckFull, c.CheckOwn)
	}
	want := "  CHECKS: the fast-forward to origin/main changed .orchestra/settings.json on main: the merge check " +
		"(check_fast) is '" + fast + "', the full check (check_full) is 'true', a worker's own check " +
		"(check_own) is 'scripts/own.sh' from now on\n"
	if ev := h.sink.text(); !strings.Contains(ev, want) {
		t.Errorf("events lack %q:\n%s", want, ev)
	}
}

// Check commands the fast-forward leaves as they were are said of nothing; a check_own it empties
// is the merge check's.
func TestTheStartsFastForwardSaysOnlyTheChecksItChanges(t *testing.T) {
	t.Parallel()
	h := harnessIn(t, t.TempDir())
	o := h.loop()
	o.cfg.Check, o.cfg.CheckFull, o.cfg.CheckOwn = "make fast", "make full", "make own"
	o.takeChecks("the fast-forward to origin/main", project.Settings{CheckFast: "make fast", CheckFull: "make full"})
	o.takeChecks("the fast-forward to origin/main", project.Settings{CheckFast: "make fast", CheckFull: "make full"})
	want := "  CHECKS: the fast-forward to origin/main changed .orchestra/settings.json on main: a worker's own " +
		"check (check_own) is the merge check from now on\n"
	if got := h.sink.text(); got != want {
		t.Errorf("events %q, want %q", got, want)
	}
	if o.cfg.CheckOwn != "" {
		t.Errorf("check_own is %q, want none", o.cfg.CheckOwn)
	}
}
