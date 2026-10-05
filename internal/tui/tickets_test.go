package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

func TestTicketRowsFollowEachTicket(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "kinieta-dwv", Title: "Reduce Motion: keep fades"},
		dispatch.Event{Kind: dispatch.EvClosed, Ticket: "kinieta-dwv", Detail: "ffd6ce4 merged into batch"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "kinieta-vzg", Title: "Competing timelines"},
		dispatch.Event{Kind: dispatch.EvDeferred, Ticket: "kinieta-vzg", Detail: "still open, noted for review"},
		dispatch.Event{Kind: dispatch.EvTriage, Ticket: "kinieta-vzg", Detail: "environment · high", Title: "prompt never submitted"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 3, Ticket: "kinieta-kco", Title: "Open the property model"},
	)
	if len(m.rows) != 3 || m.closed != 1 || m.deferred != 1 || m.triaged != 1 {
		t.Fatalf("rows %+v closed %d deferred %d triaged %d", m.rows, m.closed, m.deferred, m.triaged)
	}
	m.width, m.height = 70, 40
	view := ansi.Strip(m.View())
	for _, want := range []string{
		"✓ done", "kinieta-dwv", "ffd6ce4 merged", // completed: the commit, not the title
		"↷ deferred", "◆ environment · high · prompt", // triage replaces the reason (cut to fit)
		"▶ working", "Open the property model",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view lacks %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "Reduce Motion: keep fades") {
		t.Error("a completed ticket should show its commit, not its title")
	}

	m = runEvents(m, dispatch.Event{Kind: dispatch.EvStop, Text: "PAUSED: kinieta-kco"})
	final := ansi.Strip(printedEnd(m, 70))
	if !strings.Contains(final, "■ stopped") || strings.Contains(final, "to stop after the current tickets") ||
		strings.Contains(final, "Current") {
		t.Errorf("the summary after the dashboard should mark the stopped ticket, without the live parts:\n%s", final)
	}
}

func TestAskedTicketIsCountedAndShown(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Choose the licence"},
		dispatch.Event{Kind: dispatch.EvAsked, Ticket: "k-1", Detail: "q-1: Decision for k-1: MIT or Apache?"})
	m.width, m.height = 80, 40
	v := ansi.Strip(m.View())
	for _, want := range []string{"Needs you", "? 1", "? for you", "answer q-1: Decision for k-1"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
}

// Once its question is answered, an asked ticket's row goes back to working, rather than a second
// row being added, and it no longer counts as needing the maintainer.
func TestAnsweredTicketGoesBackToWorkInItsRow(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Choose the licence"},
		dispatch.Event{Kind: dispatch.EvAsked, Ticket: "k-1", Detail: "q-1: MIT or Apache?"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 2, Ticket: "k-2", Title: "Write the README"},
		dispatch.Event{Kind: dispatch.EvAnswered, Ticket: "k-1", Detail: "q-1: MIT or Apache?"},
		dispatch.Event{Kind: dispatch.EvDispatch, N: 3, Ticket: "k-1", Title: "Choose the licence"})
	if len(m.rows) != 2 || m.rows[0].id != "k-1" || m.rows[0].state != rowWorking || m.rows[0].note != "" || m.needsYou() != 0 {
		t.Fatalf("rows %+v, needs you %d; want k-1's first row working again and nothing asked", m.rows, m.needsYou())
	}
	m.width, m.height = 80, 40
	if v := ansi.Strip(m.View()); strings.Contains(v, "? for you") || !strings.Contains(v, "Choose the licence") {
		t.Errorf("view:\n%s", v)
	}
	if got := ansi.Strip(renderEvent(dispatch.Event{Kind: dispatch.EvAnswered, Ticket: "k-1", Detail: "q-1: MIT or Apache?"})); !strings.Contains(got, "↺ k-1 answered  q-1: MIT or Apache?") {
		t.Errorf("rendered %q", got)
	}
}

// An asked ticket its worker deferred in its tab shows as deferred, and no longer counts as needing
// the maintainer.
func TestAskedTicketDeferredLeavesTheAskedCount(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Choose the licence"},
		dispatch.Event{Kind: dispatch.EvAsked, Ticket: "k-1", Detail: "q-1: MIT or Apache?"},
		dispatch.Event{Kind: dispatch.EvDeferred, Ticket: "k-1", Detail: "by the worker"})
	if len(m.rows) != 1 || m.rows[0].state != rowDeferred || m.needsYou() != 0 || m.deferred != 1 {
		t.Fatalf("rows %+v, needs you %d, deferred %d; want k-1 deferred and nothing asked", m.rows, m.needsYou(), m.deferred)
	}
}

// An asked ticket whose worker closed it in its tab is adopted without a second dispatch: its row
// goes from "? for you" to done.
func TestAskedTicketAdoptedGoesToDone(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "batch"}, func() {}, func(bool) {}, func(string) {})
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "Choose the licence"},
		dispatch.Event{Kind: dispatch.EvAsked, Ticket: "k-1", Detail: "q-1: MIT or Apache?"},
		dispatch.Event{Kind: dispatch.EvAnswered, Ticket: "k-1", Detail: "q-1: MIT or Apache?"},
		dispatch.Event{Kind: dispatch.EvClosed, Ticket: "k-1", Detail: "abc123 merged into batch"})
	if len(m.rows) != 1 || m.rows[0].state != rowDone || m.needsYou() != 0 || m.closed != 1 {
		t.Fatalf("rows %+v, needs you %d, closed %d; want k-1 done and nothing asked", m.rows, m.needsYou(), m.closed)
	}
}
