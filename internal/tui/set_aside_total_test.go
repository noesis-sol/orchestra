package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Tickets set aside for failing at once, then reopened by the hold for the environment, are back
// in the queue: Set aside no longer counts them, and one that merges later counts only as
// completed.
func TestReopenedTicketsLeaveTheSetAsideTotal(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Add the parser"},
		dispatch.Event{Kind: dispatch.EvDeferred, Ticket: "k-1", Detail: "still open, noted for review"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "k-2", Title: "Use the parser"},
		dispatch.Event{Kind: dispatch.EvDeferred, Ticket: "k-2", Detail: "still open, noted for review"})
	if n := m.setAside(); n != 2 {
		t.Fatalf("set aside %d, want 2", n)
	}
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvReopened, Ticket: "k-1", Detail: "the run held for the environment"},
		dispatch.Event{Kind: dispatch.EvReopened, Ticket: "k-2", Detail: "the run held for the environment"})
	if n := m.setAside(); n != 0 || m.rows()[0].state != rowWaiting || m.rows()[1].state != rowWaiting {
		t.Fatalf("set aside %d, rows %+v; want both waiting, none set aside", n, m.rows())
	}
	m.width, m.height = 80, 40
	if v := ansi.Strip(m.View()); !strings.Contains(v, "back in the queue: the run held for the environment") {
		t.Errorf("view lacks the reopened rows:\n%s", v)
	}
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 3, Ticket: "k-1", Title: "Add the parser"},
		dispatch.Event{Kind: dispatch.EvClosed, Ticket: "k-1", Detail: "ffd6ce4 merged into batch"})
	if m.closed != 1 || m.setAside() != 0 {
		t.Errorf("closed %d, set aside %d; want k-1 completed only", m.closed, m.setAside())
	}
	got := ansi.Strip(renderEvent(dispatch.Event{Kind: dispatch.EvReopened, Ticket: "k-2",
		Detail: "the run held for the environment"}))
	if !strings.Contains(got, "◷ k-2 reopened  back in the queue: the run held for the environment") {
		t.Errorf("rendered %q", got)
	}
}

// A ticket left for review counts under Set aside, as a deferred one does, until it merges; one
// blocked from merging counts under Needs you instead.
func TestTicketsLeftForReviewCountAsSetAside(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Add the parser"},
		dispatch.Event{Kind: dispatch.EvWarn, Ticket: "k-1", Aside: true, Detail: "checks failed"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "k-2", Title: "Use the parser"},
		dispatch.Event{Kind: dispatch.EvWarn, Ticket: "k-2", Aside: true, Detail: "closed without a commit"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 3, Ticket: "k-3", Title: "Print the tree"},
		dispatch.Event{Kind: dispatch.EvWarn, Ticket: "k-3", Aside: true, Detail: "conflicts with batch",
			Blocked: "merge conflict in a.go"})
	if m.setAside() != 2 || m.needsYou() != 1 {
		t.Fatalf("set aside %d, needs you %d; want k-1 and k-2 set aside, k-3 needing you", m.setAside(), m.needsYou())
	}
	m.width, m.height = 100, 40
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "Set aside") || !strings.Contains(v, "↷ 2") || strings.Contains(v, "Deferred") {
		t.Errorf("totals don't count the two left for review under Set aside:\n%s", v)
	}
	// Its check run again once the base moved on, k-1 merges.
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvClosed, Ticket: "k-1", Detail: "ffd6ce4 merged into batch"})
	if m.closed != 1 || m.setAside() != 1 {
		t.Errorf("closed %d, set aside %d; want k-1 completed, k-2 set aside", m.closed, m.setAside())
	}
	m.width = 40 // the totals on one line
	if line := ansi.Strip(m.statsLine(m.width)); !strings.Contains(line, "↷ 1") {
		t.Errorf("one-line totals %q, want one set aside", line)
	}
}
