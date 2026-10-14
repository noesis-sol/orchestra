package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// orchestra-74b2.33: once the terminal has refused the arguments every worker gets, with nothing
// left to drop, the start stops at once rather than ask again with the same arguments.
func TestRefusedArgumentsWithNothingToDropStopTheStartAtOnce(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newTimedHarness(t)
		h.cfg.LaunchPrompt, h.cfg.WorkerArgs = false, []string{"--no-chrome"}
		h.herdr.refuseArgs = true
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		o, code := h.run()
		want := `START_FAILED for A in tab tab1: the terminal refused the arguments "--no-chrome" (`
		if code != ExitTool || !strings.HasPrefix(o.Final(), want) {
			t.Fatalf("exit %d, final %q, want it to start %q\n%s", code, o.Final(), want, h.sink.text())
		}
		got := h.herdr.argsFor("A")
		if len(got) != 2 || !equal(got[1], []string{"start", "--no-chrome"}) {
			t.Errorf("started with %q; want twice: with its rules, then with only --no-chrome", got)
		}
	})
}
