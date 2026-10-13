package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// The deferred tickets whose blockers have merged close the message, each with the command that
// reopens it and the one that removes its empty worktree, in a run of all of bd ready or a scoped
// one.
func TestNothingListsTheDeferredTicketsWhoseBlockersMerged(t *testing.T) {
	free := []dispatch.DeferredFree{
		{ID: "k-4", Blockers: []string{"k-2", "k-3"}, Worktree: "/w/k-4"},
		{ID: "k-5", Blockers: []string{"k-2"}},
	}
	lines := []string{
		"Deferred: k-4: its blockers have merged (k-2, k-3); to run it: bd update k-4 --status open",
		"  its worktree has no commits of its own, safe to remove: git worktree remove /w/k-4 && git branch -d wt/k-4",
		"Deferred: k-5: its blockers have merged (k-2); to run it: bd update k-5 --status open",
	}
	for _, tc := range []struct {
		name string
		n    dispatch.NothingToRun
		want []string
	}{
		{"all of bd ready", dispatch.NothingToRun{Deferred: 2, DeferredFree: free},
			append([]string{"2 deferred"}, lines...)},
		{"scoped", dispatch.NothingToRun{Scope: "k-1", NotDone: []dispatch.NotDone{{ID: "k-4", Why: "deferred"}},
			DeferredFree: free}, append([]string{"k-4 (deferred)"}, lines...)},
	} {
		_, rest := nothingLines(tc.n, false)
		if strings.Join(rest, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("%s: got %q, want %q", tc.name, rest, tc.want)
		}
		box := strings.Join(strings.Fields(ansi.Strip(nothingBox(tc.n, 200))), " ")
		for _, l := range tc.want {
			if !strings.Contains(box, strings.Join(strings.Fields(l), " ")) {
				t.Errorf("%s: the box lacks %q:\n%s", tc.name, l, box)
			}
		}
	}
}
