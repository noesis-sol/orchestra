package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Ready tickets held back follow the queue's count in the totals, as "· 4 held back", in the strip
// and in the one-line form of a narrow pane alike; with none held back, the totals read as before.
func TestTheTotalsCountTheTicketsHeldBack(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "main"}, func() {}, func(bool) {}, func(string) {})
	m.height = 40
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvQueue,
		QueueState: dispatch.QueueState{Queued: 0, Unclosed: 29, Held: dispatch.HeldBack{Overlap: 3, Merge: 1}}})
	for _, tc := range []struct {
		w    int
		want string
	}{
		{120, "0 of 29 · 4 held back"},
		{80, "0 of 29 · 4 held back"},
		{64, "0 of 29 · 4 held back"},
		{56, "queue 0 of 29 · 4 held back"},
		{40, "queue 0 of 29 · 4 he…"},
	} {
		m.width = tc.w
		view := m.View()
		for line := range strings.SplitSeq(view, "\n") {
			if ansi.StringWidth(line) > tc.w {
				t.Errorf("width %d: line is %d wide: %q", tc.w, ansi.StringWidth(line), ansi.Strip(line))
			}
		}
		if plain := ansi.Strip(view); !strings.Contains(plain, tc.want) {
			t.Errorf("width %d: no %q in\n%s", tc.w, tc.want, plain)
		}
	}

	m = runEvents(m, dispatch.Event{Kind: dispatch.EvQueue, QueueState: dispatch.QueueState{Queued: 2, Unclosed: 29}})
	for _, w := range []int{120, 64} {
		m.width = w
		if plain := ansi.Strip(m.View()); strings.Contains(plain, "held back") || !strings.Contains(plain, "2 of 29") {
			t.Errorf("width %d: none held back, yet\n%s", w, plain)
		}
	}
}

// A dispatch gives the tickets held back too.
func TestADispatchCountsTheTicketsHeldBack(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Base: "main"}, func() {}, func(bool) {}, func(string) {})
	m.width, m.height = 120, 40
	m = runEvents(m, dispatch.Event{Kind: dispatch.EvDispatch, N: 1, Ticket: "k-1", Title: "First",
		QueueState: dispatch.QueueState{Queued: 1, Unclosed: 9, Held: dispatch.HeldBack{Parent: 1, Solo: 1}}})
	if plain := ansi.Strip(m.View()); !strings.Contains(plain, "1 of 9 · 2 held back") {
		t.Errorf("no held back count in\n%s", plain)
	}
}
