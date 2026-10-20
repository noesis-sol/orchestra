package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The load line counts the workers at work against the slots: a ticket merging, waiting to merge
// or handed back to its worker frees its slot, and is shown in the Current list all the same.
func TestWorkersCountsOnlyThoseAtWork(t *testing.T) {
	m := run(t).config(dispatch.Config{Limit: 40, Concurrency: 2}).
		running(dispatch.Status{Ticket: "bd-1", Title: "At work"},
			dispatch.Status{Ticket: "bd-2", Title: "Merging", Merge: dispatch.MergeRunning},
			dispatch.Status{Ticket: "bd-3", Title: "Waiting to merge", Merge: dispatch.MergeQueued, Place: 1},
			dispatch.Status{Ticket: "bd-4", Title: "Resolving", Resolving: true}).
		sized(120, 40).dash()
	if line := ansi.Strip(m.loadLine(120)); !strings.Contains(line, "▶ 1 of 2 workers") {
		t.Errorf("the load line doesn't read \"▶ 1 of 2 workers\": %q", line)
	}
	view := ansi.Strip(m.View())
	for _, title := range []string{"At work", "Merging", "Waiting to merge", "Resolving"} {
		if !strings.Contains(view, title) {
			t.Errorf("the Current list lacks %q:\n%s", title, view)
		}
	}
}
