package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// orchestra-fa1m: what a failed check said was kept for the reviewer after a later check of the
// ticket passed, so a ticket set aside afterwards for something else, a conflict here, was listed
// among those set aside after their check failed, its fixed failure quoted as the reason.
func TestAFixedCheckFailureIsNotTheReasonATicketIsSetAside(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		// The fix passes the check, and main moves on meanwhile with a change that conflicts with A's.
		fixesThenConflicts := func(w *fakeWorker) AgentState {
			state := commitsFix(dir, "admin_test.go", true)(w)
			w.git.conflict("wt/A", "shared.txt")
			w.git.commit("main", "landed on main: shared.txt", "shared.txt")
			return state
		}
		h := checkHandBackHarness(t, dir, fixesThenConflicts)
		o, code := h.run()
		if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
			t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
		}
		ev := h.sink.text()
		for _, want := range []string{
			"'" + h.cfg.Check + "' passes on wt/A as its worker fixed it",
			"MERGE_CONFLICT: A closed, but wt/A conflicts with main",
		} {
			if !strings.Contains(ev, want) {
				t.Errorf("events lack %q:\n%s", want, ev)
			}
		}
		in := o.reviewInput(t.Context(), code, o.Final())
		if strings.Contains(in, "set aside after its check failed") {
			t.Errorf("the reviewer is told of A's fixed check failure:\n%s", in)
		}
	})
}
