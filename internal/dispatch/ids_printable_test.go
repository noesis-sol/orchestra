package dispatch

import (
	"strings"
	"testing"
)

// An ID git takes in a branch name but with a character that can't be shown, a C1 control or a
// format character, is refused: it would name a branch, a worktree and a tab, and reach the screen.
func TestIDProblemRefusesUnprintableCharacters(t *testing.T) {
	t.Parallel()
	const unprintable = "IDs with control or invisible characters can't be run"
	for id, want := range map[string]string{
		"émile-q2":          "",
		"x-\u65e5\u672c":    "",
		"x-\u0080":          unprintable,
		"x-\u009b31m":       unprintable, // the 8-bit CSI
		"x-\u009f":          unprintable,
		"x-\u202eabc":       unprintable, // right-to-left override
		"x-a\u200bb":        unprintable, // zero-width space
		"x-\u00a0":          unprintable, // no-break space
		"x-\U000e0001":      unprintable, // language tag
		"x-\x1b[31m":        "IDs that can't name a git branch can't be run",
		"x-\u009b..\u009b":  "IDs with path characters can't be run",
		"x-\u009b/\u009bab": "IDs with path characters can't be run",
	} {
		if got := IDProblem(id); got != want {
			t.Errorf("IDProblem(%q) = %q, want %q", id, got, want)
		}
	}
}

// A ready ticket whose ID carries a C1 control is set aside without a worker, and the run goes on.
func TestTicketWithC1ControlInIDIsSetAside(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	bad := "x-\u009b31m"
	h.beads.add(bad, "first", 1)
	h.beads.add("B", "second", 2)
	h.worker("B", finishes("b.txt"))

	o, code := h.run()
	if code != ExitOK {
		t.Fatalf("exit %d, final %q\n%s", code, o.Final(), h.sink.text())
	}
	if args := h.herdr.argsFor(bad); len(args) != 0 {
		t.Errorf("a worker was started for %q: %q", bad, args)
	}
	if st := h.statusOf(bad); st != "deferred" {
		t.Errorf("%q is %s, want deferred", bad, st)
	}
	if branches := h.git(h.repo, "branch", "--list", "wt/x*"); strings.TrimSpace(branches) != "" {
		t.Errorf("a branch was made for %q: %s", bad, branches)
	}
	if st := h.statusOf("B"); st != "closed" {
		t.Errorf("B is %s, want closed: the run goes on", st)
	}
}
