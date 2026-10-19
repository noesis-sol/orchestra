package dispatch

import (
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A project with a check of a worker's own change (check_own) has its workers' rules name that one
// as the check to run once with its output kept, rather than the merge check orchestra runs itself.
func TestClaudeWorkerRulesNameTheWorkersOwnCheck(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.Check, h.cfg.CheckOwn = "true", "scripts/check-own.sh"
		h.beads.add("A", "first", 1)
		var rules string
		h.worker("A", func(w *fakeWorker) AgentState {
			rules = readRun(t, w.wt, project.RulesName)
			return finishes("a.txt")(w)
		})
		if o, code := h.run(); code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		want := "as in 'scripts/check-own.sh > .orchestra/run/worker-check.log 2>&1; echo \"exit $?\"'"
		if !strings.Contains(rules, want) {
			t.Errorf("the rules lack %q:\n%s", want, rules)
		}
		if strings.Contains(rules, "as in 'true >") {
			t.Errorf("the rules name the merge check:\n%s", rules)
		}
	})
}

// The files a worker's own check names are left out of a ticket's footprint, as the merge and full
// checks' are: a ticket asks for it to pass without changing it.
func TestTicketFootprintLeavesOutTheOwnChecksFiles(t *testing.T) {
	t.Parallel()
	tracked := append(slices.Clone(trackedWithCheck), "scripts/check-own.sh")
	text := Ticket{Description: "Say hello in internal/tui/run.go.",
		AcceptanceCriteria: "scripts/check-own.sh passes."}
	c := Config{Check: "scripts/check.sh -short", CheckOwn: "scripts/check-own.sh"}
	if fp := TicketFootprint(text, tracked, checkCommands(c)); !slices.Equal(fp.Files, []string{"internal/tui/run.go"}) {
		t.Errorf("files %q, want only internal/tui/run.go", fp.Files)
	}
}
