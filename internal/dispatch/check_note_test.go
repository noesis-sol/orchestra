package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A Claude worker's rules have it run a check once, its whole output kept in .orchestra/run/ and its
// exit status printed, rather than again to see what it said: the example is the project's merge
// check where it has one, and a placeholder where it has none.
func TestClaudeWorkerRulesHaveItRunACheckOnce(t *testing.T) {
	t.Parallel()
	for _, check := range []string{"true", ""} {
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.cfg.Check = check
			h.beads.add("A", "first", 1)
			var rules string
			h.worker("A", func(w *fakeWorker) AgentState {
				rules = readRun(t, w.wt, project.RulesName)
				return finishes("a.txt")(w)
			})
			if o, code := h.run(); code != ExitOK {
				t.Fatalf("check %q: exit %d, final %q\n%s", check, code, o.Final(), h.sink.text())
			}
			cmd := check
			if cmd == "" {
				cmd = "<the check>"
			}
			for _, want := range []string{
				"Run a check (tests, linters, a build) once, its whole output saved to a file in .orchestra/run " +
					"(never committed) and its exit status printed, as in '" + cmd +
					" > .orchestra/run/worker-check.log 2>&1; echo \"exit $?\"'",
				"rather than running the check again to see its output or whether it passed",
				"Piping a check through tail hides its exit status.",
			} {
				if !strings.Contains(rules, want) {
					t.Errorf("check %q: the rules lack %q:\n%s", check, want, rules)
				}
			}
		})
	}
}
