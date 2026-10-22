package agentkind

import (
	"slices"
	"testing"
)

// A launch of every kind runs with orchestra's PATH; one of Claude Code or Codex leaves out the markers
// of the Claude Code session that may have started its terminal, and one of a kind orchestra doesn't
// know is given the PATH alone.
func TestEnvSetsPathAndLeavesOutAClaudeSession(t *testing.T) {
	session := []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "CLAUDE_CODE_CHILD_SESSION"}
	for _, tc := range []struct {
		kind Kind
		want []string
	}{
		{Claude, append(slices.Clone(session), "PATH=/o/bin:/usr/bin")},
		{Codex, append(slices.Clone(session), "PATH=/o/bin:/usr/bin")},
		{Of("aider"), []string{"PATH=/o/bin:/usr/bin"}},
		{Named(""), []string{"PATH=/o/bin:/usr/bin"}},
	} {
		if got := tc.kind.Env("/o/bin:/usr/bin"); !slices.Equal(got, tc.want) {
			t.Errorf("%q's Env = %q, want %q", tc.kind.Name, got, tc.want)
		}
	}
	// Env gives each launch a list of its own, which leaves the kind's row as it was.
	env := Claude.Env("/a")
	env[0] = "X"
	if Claude.Unset[0] != "CLAUDECODE" {
		t.Errorf("Env changed the kind's row: %q", Claude.Unset)
	}
}
