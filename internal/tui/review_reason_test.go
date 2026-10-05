package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A warning that sets its ticket aside with a reason shows the reason on the row, as a deferred
// ticket shows its own, in place of sending the maintainer to the log. (A MERGE_CONFLICT shows
// blocked: blocked_test.go.)
func TestReviewRowShowsWhyTheTicketWasSetAside(t *testing.T) {
	for _, tc := range []struct{ why, text string }{
		{"checks failed", "  CHECKS_FAILED: k-1 closed, but 'scripts/check.sh' fails on wt/k-1 rebased onto batch; …"},
		{"closed without a commit", "  CLOSED_WITHOUT_COMMIT: no commit on wt/k-1 names k-1; worktree wt and tab t1 left for review"},
	} {
		t.Run(tc.why, func(t *testing.T) {
			m := runEvents(reviewDashboard(),
				dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Repeat a timeline"},
				dispatch.Event{Kind: dispatch.EvWarn, Ticket: "k-1", Aside: true, Detail: tc.why, Text: tc.text})
			v := ansi.Strip(m.View())
			if m.rows[0].state != rowReview || !strings.Contains(v, "! review") || !strings.Contains(v, tc.why) ||
				strings.Contains(v, "see the log") {
				t.Errorf("k-1 should show for review, saying %q:\n%s", tc.why, v)
			}
		})
	}
}

// A long reason is cut to the row's width rather than wrapping the table.
func TestReviewRowCutsALongReason(t *testing.T) {
	why := "worker finished without closing; see Herdr tab t1 and worktree /Users/someone/worktrees/k-1"
	m := runEvents(reviewDashboard(),
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Repeat a timeline"},
		dispatch.Event{Kind: dispatch.EvWarn, Ticket: "k-1", Aside: true, Detail: why, Text: "  DEFER_FAILED: …"})
	v := ansi.Strip(m.View())
	var row string
	for l := range strings.SplitSeq(v, "\n") {
		if strings.Contains(l, "! review") {
			row = l
		}
	}
	if row == "" || !strings.Contains(row, "worker finished without closing") || !strings.Contains(row, "…") ||
		ansi.StringWidth(row) > m.width {
		t.Errorf("k-1's row should cut its reason to fit %d columns:\n%s", m.width, v)
	}
}
