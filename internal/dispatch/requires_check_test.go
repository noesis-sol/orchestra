package dispatch

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A run whose requirements are met starts as it would without them, saying so; one with a check that
// fails runs its install in the main checkout, checks again, and goes on.
func TestRequirementsMetOrInstalledStartTheRun(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Requires = []project.Requirement{
			{Name: "sh", Check: "command -v sh", Manual: "install a shell"},
			{Name: "tool", Check: "test -e tool", Install: "touch tool"},
		}
		h.beads.add("A", "works", 1)
		h.worker("A", finishes("a.txt"))
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if !exists(filepath.Join(h.cfg.Repo, "tool")) {
			t.Error("the install didn't run in the main checkout")
		}
		ev := h.sink.text()
		for _, want := range []string{"  REQUIRES: installing tool: touch tool\n",
			"  REQUIRES: sh met; tool (installed with 'touch tool') installed\n"} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
	})
}

// What is still missing as the run starts, a manual step or a failed install, stops it before its first
// ticket: REQUIREMENTS_MISSING, naming each with what to do, exit 2, and no worker started.
func TestRequirementsMissingStopTheRunBeforeItsFirstTicket(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Requires = []project.Requirement{
			{Name: "docker", Check: "false", Manual: "start Docker Desktop"},
			{Name: "tool", Check: "test -e tool", Install: "echo no such package; exit 1"},
		}
		h.beads.add("A", "would fail on it", 1)
		started := false
		h.worker("A", func(w *fakeWorker) AgentState {
			started = true
			return finishes("a.txt")(w)
		})
		o, code := h.run()
		output := filepath.Join(h.cfg.Repo, project.RunPath(project.RequireLogName("tool")))
		want := "REQUIREMENTS_MISSING: docker (start Docker Desktop); tool ('echo no such package; exit 1' failed: " +
			"exit status 1; its output is in " + output + "); meet them (each entry of 'requires' in " +
			".orchestra/settings.json says how), then run again"
		if code != ExitSetup || o.Final() != want {
			t.Fatalf("exit %d, final:\n%s\nwant exit %d, final:\n%s\n%s", code, o.Final(), ExitSetup, want, h.sink.text())
		}
		if st, err := h.beads.Status(t.Context(), "A"); started || err != nil || st != StatusOpen {
			t.Errorf("A's worker started %v; A is %s (%v)", started, st, err)
		}
		if got := read(t, output); got != "no such package\n" {
			t.Errorf("the install's output: %q", got)
		}
	})
}

// A merge that changes the requirements in .orchestra/settings.json has the run check them before the
// next ticket starts: here a manual one the machine lacks, which stops the run before B, as at its
// start. A merge that leaves them as they are checks nothing again.
func TestAMergeThatChangesTheRequirementsChecksThemAgain(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Requires = []project.Requirement{{Name: "sh", Check: "command -v sh", Manual: "install a shell"}}
		same, err := json.Marshal(project.Settings{Requires: h.cfg.Requires, Setup: "true"})
		if err != nil {
			t.Fatal(err)
		}
		more, err := json.Marshal(project.Settings{Requires: append(h.cfg.Requires,
			project.Requirement{Name: "db", Check: "false", Manual: "start the database"})})
		if err != nil {
			t.Fatal(err)
		}
		h.beads.add("A", "changes the setup alone", 1)
		h.beads.add("B", "adds a requirement", 2)
		h.beads.add("C", "needs it", 3)
		h.beads.link("B", "A", "blocks")
		h.beads.link("C", "B", "blocks")
		commits := func(id, text string) behaviour {
			return func(w *fakeWorker) AgentState {
				w.claim()
				if err := h.mem.commitFile(w.wt, id+": change the settings", project.SettingsFile, text); err != nil {
					t.Error(err)
				}
				w.close()
				return "idle"
			}
		}
		h.worker("A", commits("A", string(same)))
		h.worker("B", commits("B", string(more)))
		started := false
		h.worker("C", func(w *fakeWorker) AgentState {
			started = true
			return finishes("c.txt")(w)
		})
		o, code := h.run()
		if code != ExitSetup || !strings.HasPrefix(o.Final(), "REQUIREMENTS_MISSING: db (start the database); meet it ") {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		if started {
			t.Error("C started with a requirement missing")
		}
		ev := h.sink.text()
		changed := "  REQUIRES: B's merge changed the requirements in .orchestra/settings.json on main: " +
			"they are checked before the next ticket starts\n"
		if strings.Count(ev, "REQUIRES: checking 1 requirement\n") != 1 || strings.Contains(ev, "A's merge changed the req") ||
			!strings.Contains(ev, changed) || strings.Count(ev, "REQUIRES: checking 2 requirements\n") != 1 {
			t.Errorf("events: want the requirements checked as the run starts and after B's merge alone:\n%s", ev)
		}
	})
}
