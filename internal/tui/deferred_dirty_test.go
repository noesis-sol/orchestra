package tui

import (
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A deferred ticket whose worktree holds uncommitted changes gets, in place of the command that
// removes it, the one that shows them.
func TestNothingKeepsADeferredTicketsDirtyWorktree(t *testing.T) {
	n := dispatch.NothingToRun{Deferred: 1, DeferredFree: []dispatch.DeferredFree{
		{ID: "k-4", Blockers: []string{"k-2"}, Uncommitted: "/w/k-4"},
	}}
	want := []string{
		"1 deferred",
		"Deferred: k-4: its blockers have merged (k-2); to run it: bd update k-4 --status open",
		"  its worktree holds uncommitted changes, not safe to remove: git -C /w/k-4 status",
	}
	if _, rest := nothingLines(n, false); strings.Join(rest, "\n") != strings.Join(want, "\n") {
		t.Errorf("got %q, want %q", rest, want)
	}
}
