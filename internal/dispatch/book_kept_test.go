package dispatch

import "testing"

// TestTicketBookKeepsWhatTheRunLearns pins the records' failed check, set-up commit, parent and resumed
// session: each is reset by the transition that ends what it records, and a record left holding
// nothing is dropped, so a passing check leaves no stale record behind.
func TestTicketBookKeepsWhatTheRunLearns(t *testing.T) {
	t.Parallel()
	var b ticketBook
	noRecord := func(step, id string) {
		t.Helper()
		if r, ok := b.get(id); ok {
			t.Errorf("%s: %s still has a record: %+v", step, id, r)
		}
		if err := b.check(); err != nil {
			t.Errorf("%s: %v", step, err)
		}
	}

	b.checkFailed("A", checkFail{said: []string{"--- FAIL: TestA"}})
	if r, _ := b.get("A"); r.failedCheck == nil || r.failedCheck.said[0] != "--- FAIL: TestA" {
		t.Errorf("a failed check: A's record is %+v", r)
	}
	b.forgetCheck("A")
	noRecord("the check passes", "A")

	b.setAside("B")
	b.checkFailed("B", checkFail{said: []string{"FAIL"}})
	b.forgetCheck("B")
	if r, _ := b.get("B"); r.failedCheck != nil || r.disp != dispAside {
		t.Errorf("set aside, its check passing later: B's record is %+v", r)
	}

	if from := b.setUpSince("S", ""); from != "" {
		t.Errorf("never set up, with no commit before: %q", from)
	}
	noRecord("never set up", "S")
	if from := b.setUpSince("S", "c0"); from != "c0" {
		t.Errorf("never set up: from %q, want the commit before its rebase", from)
	}
	b.setUp("S", "c1")
	b.setUp("S", "") // git couldn't say: nothing changes
	if from := b.setUpSince("S", "c2"); from != "c1" {
		t.Errorf("set up for c1: from %q", from)
	}
	b.freshWorktree("S")
	noRecord("a fresh worktree", "S")

	b.setParent("P", "")
	noRecord("no parent", "P")
	b.setParent("C", "P")
	b.merged("C")
	if r, _ := b.get("C"); r.parent != "P" {
		t.Errorf("merged: C's parent is %q; its parent's close reads it", r.parent)
	}

	if !b.resumeOnce("R") || b.resumeOnce("R") {
		t.Error("a worker's session is resumed once in a run")
	}
	if err := b.check(); err != nil {
		t.Error(err)
	}
}
