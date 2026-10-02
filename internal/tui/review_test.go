package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

func reviewDashboard() Dashboard {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m.width, m.height = 80, 40
	return m
}

// A warning about a ticket still running leaves its row working, with what its worker is doing,
// rather than marking it for review while the Current box shows it at work.
func TestWarningsAboutARunningTicketKeepItsRowWorking(t *testing.T) {
	m := runEvents(reviewDashboard(), dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Repeat a timeline"})
	next, _ := m.Update(statusMsg(dispatch.Status{Ticket: "k-1", Title: "Repeat a timeline", Started: time.Now(),
		Agent: dispatch.StateWorking, Doing: "testing"}))
	m = runEvents(next.(Dashboard),
		dispatch.Event{Kind: dispatch.EvWarn, Ticket: "k-1",
			Text: "  LONG_RUNNING: k-1 still working after 2h in tab t1; still waiting on it, as no ticket limit is set"},
		dispatch.Event{Kind: dispatch.EvWarn, Ticket: "k-1",
			Text: "  WATCH_FAILED for k-1: panic: oops; its status is not shown until it takes its prompt"})
	v := ansi.Strip(m.View())
	if m.rows[0].state != rowWorking || !strings.Contains(v, "▶ testing") || strings.Contains(v, "! review") {
		t.Errorf("k-1 should still show as testing, not for review:\n%s", v)
	}
}

// A failed triage leaves a deferred ticket deferred, with the deferral's reason.
func TestTriageFailureKeepsTheDeferralsReason(t *testing.T) {
	m := runEvents(reviewDashboard(),
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Repeat a timeline"},
		dispatch.Event{Kind: dispatch.EvDeferred, Ticket: "k-1", Detail: "still open, noted for review"},
		dispatch.Event{Kind: dispatch.EvWarn, Ticket: "k-1", Text: "  TRIAGE_FAILED for k-1: claude: exit status 1"})
	v := ansi.Strip(m.View())
	if m.rows[0].state != rowDeferred || !strings.Contains(v, "↷ deferred") ||
		!strings.Contains(v, "still open, noted for review") || strings.Contains(v, "! review") {
		t.Errorf("k-1 should stay deferred with its reason:\n%s", v)
	}
}

// A warning that sets its ticket aside marks the row for review, sending the maintainer to the log
// when it gives no reason.
func TestWarningsThatSetATicketAsideShowItForReview(t *testing.T) {
	for _, text := range []string{
		"  CLOSED_WITHOUT_COMMIT: no commit on wt/k-1 names k-1; worktree wt and tab t1 left for review",
		"  CHECKS_FAILED: k-1 closed, but 'scripts/check.sh' fails on wt/k-1 rebased onto batch; …",
		"  MERGE_CONFLICT: k-1 closed, but wt/k-1 conflicts with batch, which moved on while it ran; …",
		"  DEFER_FAILED: k-1 still open, and bd could not defer it; kept out of this run, …",
	} {
		code, _, _ := strings.Cut(strings.TrimSpace(text), ":")
		t.Run(code, func(t *testing.T) {
			m := runEvents(reviewDashboard(),
				dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Repeat a timeline"},
				dispatch.Event{Kind: dispatch.EvWarn, Ticket: "k-1", Aside: true, Text: text})
			v := ansi.Strip(m.View())
			if m.rows[0].state != rowReview || !strings.Contains(v, "! review") ||
				!strings.Contains(v, "left for review, see the log") {
				t.Errorf("k-1 should show for review:\n%s", v)
			}
		})
	}
}

// A ticket deferred, brought back in the same run and deferred again shows the new reason, not
// the first deferral's verdict, even one in only after the ticket came back.
func TestTicketDeferredAgainShowsTheNewReason(t *testing.T) {
	first := []dispatch.Event{
		{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Repeat a timeline"},
		{Kind: dispatch.EvDeferred, Ticket: "k-1", Detail: "still open, noted for review"},
	}
	verdict := dispatch.Event{Kind: dispatch.EvTriage, Ticket: "k-1", Detail: "environment · high", Title: "the simulator is missing"}
	back := dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "k-1", Title: "Repeat a timeline"} // reopened after a hold
	again := dispatch.Event{Kind: dispatch.EvDeferred, Ticket: "k-1", Detail: "by the worker"}
	for name, evs := range map[string][]dispatch.Event{
		"verdict before it came back": append(first[:2:2], verdict, back, again),
		"verdict after it came back":  append(first[:2:2], back, verdict, again),
	} {
		t.Run(name, func(t *testing.T) {
			m := runEvents(reviewDashboard(), evs...)
			v := ansi.Strip(m.View())
			if len(m.rows) != 1 || !strings.Contains(v, "by the worker") || strings.Contains(v, "simulator") || m.triaged != 1 {
				t.Errorf("rows %+v, triaged %d; want k-1's one row with the new reason:\n%s", m.rows, m.triaged, v)
			}
			m = runEvents(m, dispatch.Event{Kind: dispatch.EvTriage, Ticket: "k-1", Detail: "ticket · high", Title: "needs a decision"})
			if v := ansi.Strip(m.View()); !strings.Contains(v, "◆ ticket · high · needs a decision") {
				t.Errorf("the new verdict should replace the reason:\n%s", v)
			}
		})
	}
}
