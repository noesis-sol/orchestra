package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Ready tickets the run holds back have lines of their own, after those waiting on other tickets,
// as bd blocked doesn't list them.
func TestNothingReadySaysWhatTheHeldTicketsWaitFor(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    dispatch.NothingToRun
		want []string
	}{
		{"one each", dispatch.NothingToRun{Waiting: 1, HeldParents: 1, HeldBlocked: 1, Unmerged: 1}, []string{
			"1 ticket waits on other tickets: bd blocked",
			"1 ticket waits for its subtickets to merge",
			"1 ticket waits for its blocker to merge",
			"1 closed but not merged: bd list --label unmerged"}},
		{"several", dispatch.NothingToRun{HeldParents: 2, HeldBlocked: 3, InProgress: 1}, []string{
			"2 tickets wait for their subtickets to merge",
			"3 tickets wait for their blockers to merge",
			"1 in progress"}},
	} {
		head, rest := nothingLines(tc.n, false)
		if head != "○ Nothing ready to run" || strings.Join(rest, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("%s: got %q %q, want %q", tc.name, head, rest, tc.want)
		}
		box := ansi.Strip(nothingBox(tc.n, 80))
		for _, l := range tc.want {
			if !strings.Contains(box, l) {
				t.Errorf("%s: the box lacks %q:\n%s", tc.name, l, box)
			}
		}
	}
}
