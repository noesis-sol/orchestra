package dispatch

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
)

// A ticket checked ahead of its turn that can't merge as checked says why in its turn: the branch ahead
// didn't merge as it was checked (TestTheNextTicketIsCheckedAgainWhenTheOneBeforeItFails), or it did, and
// the ticket's own check failed on top of it, as here: A's check passes once B's has started, and B's
// fails ahead of its turn and passes in it.

// blameCheck counts each ticket's runs in dir, as trainCheck does: on A it passes once B's check has
// started beside it, waiting for it for up to 10 seconds; on B it fails on its first run alone.
func blameCheck(dir string) string {
	return fmt.Sprintf(`id=$(basename "$PWD"); echo run >> '%[1]s'/$id.runs; `+
		`if [ "$id" = A ]; then i=0; while [ ! -f '%[1]s'/B.runs ] && [ $i -lt 200 ]; do sleep 0.05; i=$((i+1)); done; `+
		`test -f '%[1]s'/B.runs; else test "$(wc -l < '%[1]s'/B.runs)" -gt 1; fi`, dir)
}

func TestATicketWhoseOwnCheckFailedAheadOfItsTurnSaysSo(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		h, g, o := trainLoop(t, 1, blameCheck(dir))
		code := o.Run(context.Background())
		ev := h.sink.text()
		if code != ExitOK {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), ev)
		}
		if got := closedIDs(h); !equal(got, []string{"A", "B"}) {
			t.Fatalf("merged %v, want A then B\n%s", got, ev)
		}
		a := commitOf(h.mem, "main", "A: add a.txt")
		if onto := g.rebasedOnto(h.worktree("B")); len(onto) != 2 || onto[1] != a {
			t.Errorf("B was rebased onto %v, want onto A's branch ahead of its turn, then onto main's %s", onto, a)
		}
		for _, want := range []string{
			"checking wt/B ahead of its turn to merge, rebased onto wt/A as it is checked\n",
			"wt/B was checked ahead of its turn on top of wt/A, which merged as it was checked, but '" + h.cfg.Check +
				"' fails on wt/B on top of it (output is in ",
			"): put back as its worker left it\n",
			"'" + h.cfg.Check + "' passes on the rebased wt/B\n",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		if strings.Contains(ev, "which did not merge as it was checked") {
			t.Errorf("wt/A merged as it was checked, yet the log says it didn't:\n%s", ev)
		}
		if n := checkRuns(t, dir, "B"); n != 2 {
			t.Errorf("B's check ran %d times, want 2: ahead of its turn, and in it", n)
		}
	})
}
