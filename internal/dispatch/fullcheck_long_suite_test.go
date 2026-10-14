package dispatch

import (
	"strings"
	"testing"
	"testing/synctest"
)

// Two suites whose names begin alike past the width of a title, so that their tickets' titles are
// cut to the same one, each get a ticket of their own: the next failure in either is noted on its
// own.
func TestFullCheckSuitesWithTheSameLongPrefixFileATicketEach(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const prefix = "integration tests of the very long package path "
		failsIn := func(suite string) string { return "echo 'SUITE: " + suite + "'; echo 'FAIL'; exit 1" }
		h := newTimedHarness(t)
		h.cfg.CheckFull = failsIn(prefix + "alpha")
		h.beads.add("A", "first", 1)
		h.worker("A", finishes("a.txt"))
		o, code := h.runAndCheck(t.Context())
		if code != ExitOK {
			t.Fatalf("exit %d\n%s", code, h.sink.text())
		}
		if fullCheckTitle(prefix+"alpha") != fullCheckTitle(prefix+"beta") {
			t.Fatalf("the titles should be cut to the same one: %q, %q",
				fullCheckTitle(prefix+"alpha"), fullCheckTitle(prefix+"beta"))
		}

		o.cfg.CheckFull = failsIn(prefix + "beta")
		o.FullCheck(t.Context())
		filed := filedForFullCheck(h)
		if len(filed) != 2 {
			t.Fatalf("filed %d tickets, want one for each suite: %+v", len(filed), filed)
		}

		o.cfg.CheckFull = failsIn(prefix + "alpha")
		o.FullCheck(t.Context())
		if again := filedForFullCheck(h); len(again) != 2 {
			t.Errorf("filed a third ticket: %+v", again)
		}
		for _, bug := range filed {
			notes := h.beads.notesOf(bug.ID)
			alpha := strings.Contains(bug.Description, "in the suite "+prefix+"alpha,")
			if noted := notes != ""; noted != alpha {
				t.Errorf("%s (%q) noted: %v, want %v; notes %q", bug.ID, bug.Description, noted, alpha, notes)
			}
		}
	})
}
