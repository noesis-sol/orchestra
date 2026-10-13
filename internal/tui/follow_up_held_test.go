package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Follow-ups held for the ticket they were filed from to merge have a line of their own, after
// the tickets waiting for their blockers.
func TestNothingReadySaysFollowUpsWaitForTheirSource(t *testing.T) {
	for _, tc := range []struct {
		name string
		n    dispatch.NothingToRun
		want []string
	}{
		{"one", dispatch.NothingToRun{HeldFollowUps: 1, Unmerged: 1}, []string{
			"1 follow-up waits for the ticket it was filed from to merge",
			"1 closed but not merged: bd list --label unmerged"}},
		{"several", dispatch.NothingToRun{HeldBlocked: 1, HeldFollowUps: 2, Unmerged: 2}, []string{
			"1 ticket waits for its blocker to merge",
			"2 follow-ups wait for the tickets they were filed from to merge",
			"2 closed but not merged: bd list --label unmerged"}},
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
