package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The Workers box counts the workers at work against the slots: a ticket merging, waiting to merge
// or handed back to its worker frees its slot, and is shown in the Current list all the same.
func TestWorkersCountsOnlyThoseAtWork(t *testing.T) {
	m := NewDashboard(dispatch.Config{Limit: 40, Concurrency: 2}, func() {}, func(bool) {}, func(string) {})
	m.width, m.height = 120, 40
	m.active = map[string]dispatch.Status{
		"bd-1": {Ticket: "bd-1", Title: "At work"},
		"bd-2": {Ticket: "bd-2", Title: "Merging", Merge: dispatch.MergeRunning},
		"bd-3": {Ticket: "bd-3", Title: "Waiting to merge", Merge: dispatch.MergeQueued, Place: 1},
		"bd-4": {Ticket: "bd-4", Title: "Resolving", Resolving: true},
	}
	if table := ansi.Strip(m.statsTable(120)); !strings.Contains(table, "1 of 2") {
		t.Errorf("the Workers box doesn't read \"1 of 2\": %q", table)
	}
	view := ansi.Strip(m.View())
	for _, title := range []string{"At work", "Merging", "Waiting to merge", "Resolving"} {
		if !strings.Contains(view, title) {
			t.Errorf("the Current list lacks %q:\n%s", title, view)
		}
	}
}
