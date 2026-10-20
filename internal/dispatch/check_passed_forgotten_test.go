package dispatch

import (
	"fmt"
	"strings"
	"testing"
)

// orchestra-fa1m: what a failed check said was kept for the reviewer after a later check of the
// ticket passed, so a ticket set aside afterwards for something else, a conflict here, was listed
// among those set aside after their check failed, its fixed failure quoted as the reason.
func TestAFixedCheckFailureIsNotTheReasonATicketIsSetAside(t *testing.T) {
	t.Parallel()
	h := conflictHarness(t)
	h.cfg.ResolveConflicts = false
	h.cfg.CheckHandBacks = 2
	dir := t.TempDir()
	// The check fails until the worker's fix, then passes, and as it does main moves on with a change
	// to shared.txt that conflicts with A's: the merge that follows rebases A again and stops.
	h.cfg.Check = fmt.Sprintf("test -f '%[1]s'/fixed || { echo '--- FAIL: TestAdmin (0.01s)'; exit 1; }; "+
		"test -f '%[1]s'/landed && exit 0; touch '%[1]s'/landed; echo main > '%[2]s'/shared.txt; "+
		"git -C '%[2]s' -c user.name=t -c user.email=t@t commit -qam 'landed on main: shared.txt'", dir, h.repo)
	h.beads.add("A", "first", 1)
	h.worker("A", func(w *fakeWorker) AgentState {
		w.claim()
		landsOnMain(w, h.repo, "other.txt", "other\n")
		w.commit("shared.txt")
		w.close()
		return "idle"
	}, commitsFix(dir, "admin_test.go", true))
	o, code := h.run()
	if code != ExitOK || o.Final() != "READY_EMPTY after 1 tickets" {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	ev := h.sink.text()
	for _, want := range []string{
		"FIXING: '" + h.cfg.Check + "' fails on wt/A rebased onto main",
		"'" + h.cfg.Check + "' passes on wt/A as its worker fixed it",
		"MERGE_CONFLICT: A closed, but wt/A conflicts with main",
	} {
		if !strings.Contains(ev, want) {
			t.Errorf("events lack %q:\n%s", want, ev)
		}
	}
	if r, _ := o.book.get("A"); r.failedCheck != nil {
		t.Errorf("A's record keeps its fixed check failure: %+v", r.failedCheck)
	}
	in := o.reviewInput(t.Context(), code, o.Final())
	if strings.Contains(in, "set aside after its check failed") {
		t.Errorf("the reviewer is told of A's fixed check failure:\n%s", in)
	}
}
