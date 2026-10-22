package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A ticket whose setup failed is back in the queue: its EvRequeued shows it waiting, with why, and
// the run's summary no longer lists it as working (orchestra-4n6j.6). The SETUP_FAILED warning beside
// it, which sets nothing aside, leaves the row as it is.
func TestARequeuedTicketShowsWaiting(t *testing.T) {
	m := NewDashboard(dispatch.Config{Base: "main", Concurrency: 1}, func() {}, func(bool) {}, func(string) {})
	m.height = 40
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, Ticket: "k-1", Title: "Add login"})
	m = runEvents(m,
		dispatch.Event{Kind: dispatch.EvRequeued, Ticket: "k-1", Detail: "its setup failed"},
		dispatch.Event{Kind: dispatch.EvWarn, Ticket: "k-1", Text: "  SETUP_FAILED: 'npm ci' fails in /wt/k-1 for k-1"},
		dispatch.Event{Kind: dispatch.EvDone, Text: "READY_EMPTY after 1 tickets"})
	summary := ansi.Strip(m.summary(120))
	if !strings.Contains(summary, "◷ waiting") || !strings.Contains(summary, "back in the queue: its setup failed") {
		t.Errorf("k-1 isn't shown waiting, its setup failed:\n%s", summary)
	}
	if strings.Contains(summary, "working") {
		t.Errorf("k-1 is still shown working:\n%s", summary)
	}
}

// EvRequeued has no line in the log, so the printer after the dashboard prints none for it.
func TestTheRequeueHasNoPrintedLine(t *testing.T) {
	for _, styled := range []bool{false, true} {
		var b strings.Builder
		Printer{Out: &b, Styled: styled, Width: 120}.Event(dispatch.Event{Kind: dispatch.EvRequeued, Ticket: "k-1",
			Detail: "its setup failed", Time: time.Now()})
		if b.Len() != 0 {
			t.Errorf("styled %t: printed %q", styled, b.String())
		}
	}
}
