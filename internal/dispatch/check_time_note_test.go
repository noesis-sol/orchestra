package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/noesis-sol/orchestra/internal/project"
)

// A Claude worker's rules have it give a check its time: Bash's timeout at its maximum, as Bash
// otherwise moves a check to the background after 2 minutes, and for a check that can take longer
// than that, as long as the merge check's limit, which they name, the background with a wait on
// Monitor rather than a long sleep.
func TestClaudeWorkerRulesGiveACheckItsTime(t *testing.T) {
	t.Parallel()
	for _, limit := range []time.Duration{0, 45 * time.Minute} {
		synctest.Test(t, func(t *testing.T) {
			h := newTimedHarness(t)
			h.cfg.Check, h.cfg.CheckOwn, h.cfg.CheckTimeout = "true", "scripts/check-own.sh", limit
			h.beads.add("A", "first", 1)
			var rules string
			h.worker("A", func(w *fakeWorker) AgentState {
				rules = readRun(t, w.wt, project.RulesName)
				return finishes("a.txt")(w)
			})
			if o, code := h.run(); code != ExitOK {
				t.Fatalf("limit %s: exit %d, final %q\n%s", limit, code, o.Final(), h.sink.text())
			}
			named := "30m"
			if limit != 0 {
				named = "45m"
			}
			for _, want := range []string{
				"Give a check its time: Bash waits 2 minutes on a command unless told otherwise, then moves it " +
					"to the background.",
				"Pass Bash timeout: 600000, its maximum (10 minutes), for every check.",
				"(orchestra gives its merge check up to " + named + ")",
				"run in the background (run_in_background), its exit status in the file too, as in " +
					"'scripts/check-own.sh > .orchestra/run/worker-check.log 2>&1; echo \"exit $?\" >> " +
					".orchestra/run/worker-check.log'",
				"wait on with Monitor, an until-loop that greps the file for its exit line: never with a long " +
					"sleep, which Bash refuses.",
			} {
				if !strings.Contains(rules, want) {
					t.Errorf("limit %s: the rules lack %q:\n%s", limit, want, rules)
				}
			}
		})
	}
}

// Bash's timeout and Monitor are Claude Code's: workers of other kinds aren't told of them.
func TestOtherWorkersAreNotToldOfBashsTimeout(t *testing.T) {
	t.Parallel()
	o := &Loop{cfg: Config{AgentKind: "codex", Check: "make check"}}
	if note := o.checkTimeNote(); note != "" {
		t.Errorf("a codex worker is told %q", note)
	}
}
