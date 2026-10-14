package tui

import (
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The events the dashboard takes in after its final one, before its program has exited (an organ's
// verdict, a TRIAGE_FAILED warning), would go with the alternate screen: they don't count as
// received, so Handoff prints them, below the run's summary and its last line.
func TestEventsAfterTheFinalOnePrintAfterTheSummary(t *testing.T) {
	p := tea.NewProgram(NewDashboard(dispatch.Config{}, func() {}, func(bool) {}, func(string) {}),
		tea.WithInput(nil), tea.WithOutput(io.Discard))
	p.Kill() // the sink only keeps what it sends; the dashboard below takes the messages in
	sink := NewProgramSink(p)
	now := time.Now()
	evs := []dispatch.Event{
		{Kind: dispatch.EvDispatch, N: 1, Limit: 3, Ticket: "a-1", Title: "First", Time: now},
		{Kind: dispatch.EvDone, N: 1, Limit: 3, Text: "READY_EMPTY no ready tickets", Time: now},
		{Kind: dispatch.EvWarn, Ticket: "a-1", Text: "TRIAGE_FAILED a-1: the organ timed out", Time: now},
		{Kind: dispatch.EvTriage, Ticket: "a-2", Detail: "retry", Title: "flaky", Text: "TRIAGE a-2", Time: now},
	}
	m := NewDashboard(dispatch.Config{Limit: 3}, func() {}, func(bool) {}, func(string) {})
	for _, ev := range evs {
		sink.Event(ev)
		m = runEvents(m, ev) // as the program does until the quit the final event asked for comes in
	}
	if m.Received() != 2 || m.Final() == nil || m.triaged != 0 {
		t.Fatalf("dashboard received %d, final %v, triaged %d; want 2, the done event, 0",
			m.Received(), m.Final(), m.triaged)
	}
	var out strings.Builder
	printer := Printer{Out: &out}
	printer.Summary(m)
	printer.End(m)
	sink.Handoff(printer, m.Received())
	got := out.String()
	summary, done := strings.Index(got, "Orchestra"), strings.Index(got, "READY_EMPTY")
	warn, triage := strings.Index(got, "TRIAGE_FAILED a-1"), strings.Index(got, "TRIAGE a-2")
	if summary < 0 || done < summary || warn < done || triage < warn {
		t.Errorf("want the summary, the done line, the warning, then the triage line:\n%s", got)
	}
	if strings.Count(got, "READY_EMPTY") != 1 {
		t.Errorf("the done line printed more than once:\n%s", got)
	}
}

// Ctrl+C, and the loop's end, quit the dashboard too: what comes in after them is for the printer.
func TestEventsAfterAQuitAreNotReceived(t *testing.T) {
	for name, quit := range map[string]tea.Msg{
		"ctrl+c":   tea.KeyMsg{Type: tea.KeyCtrlC},
		"finished": Finished{},
	} {
		m := NewDashboard(dispatch.Config{Limit: 3}, func() {}, func(bool) {}, func(string) {})
		m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "a-1", Title: "First"})
		next, _ := m.Update(quit)
		m = runEvents(next.(Dashboard), dispatch.Event{Kind: dispatch.EvWarn, Text: "LONG_RUNNING a-1"})
		if m.Received() != 1 {
			t.Errorf("%s: received %d, want 1", name, m.Received())
		}
	}
}
