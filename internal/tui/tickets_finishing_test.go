package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// currentInView is what follows the Current label in m's view, unstyled.
func currentInView(t *testing.T, m Dashboard) string {
	t.Helper()
	view := ansi.Strip(m.View())
	_, current, ok := strings.Cut(view, "  Current")
	if !ok {
		t.Fatalf("the view should have Current:\n%s", view)
	}
	return current
}

// A ticket's end event comes before the status that takes it out of Current: in the frame between
// the two it shows in Current alone, and once it has left, in the tickets table alone, whatever way it
// came to an end.
func TestFinishingTicketShowsInOnePlace(t *testing.T) {
	for name, end := range map[string]dispatch.Event{
		"closed":   {Kind: dispatch.EvClosed, Ticket: "k-4", Detail: "def5678 merged into batch"},
		"deferred": {Kind: dispatch.EvDeferred, Ticket: "k-4", Detail: "still open"},
		"blocked": {Kind: dispatch.EvWarn, Ticket: "k-4", Aside: true, Blocked: "merge conflict in a.go",
			Text: "MERGE_CONFLICT: k-4 closed, but wt/k-4 conflicts with batch"},
		"for review": {Kind: dispatch.EvWarn, Ticket: "k-4", Aside: true, Detail: "checks failed",
			Text: "CHECKS_FAILED: k-4"},
	} {
		t.Run(name, func(t *testing.T) {
			m := runEvents(besideCurrentDashboard(t), end)
			if table, current := tableInView(t, m), currentInView(t, m); strings.Contains(table, "k-4") ||
				!strings.Contains(current, "k-4") {
				t.Errorf("until its status says it's gone, k-4 should show in Current alone:\ntable:%s\nCurrent:%s",
					table, current)
			}
			m = setStatus(m, dispatch.Status{Ticket: "k-4", Gone: true})
			if table, current := tableInView(t, m), currentInView(t, m); !strings.Contains(table, "k-4") ||
				strings.Contains(current, "k-4") {
				t.Errorf("once gone, k-4 should show in the tickets table alone:\ntable:%s\nCurrent:%s", table, current)
			}
		})
	}
}
