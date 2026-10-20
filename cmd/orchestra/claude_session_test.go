package main

import (
	"slices"
	"testing"
)

// TestClaudeSessionArgs checks the command line every interactive session of orchestra's gets: the
// instructions, the effort when there is one, the MCP servers (none but the chosen ones) and Chrome,
// then what the session adds.
func TestClaudeSessionArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		s    claudeSession
		want []string
	}{
		{"default effort", claudeSession{}, []string{"--append-system-prompt-file", "/prompt.md",
			"--strict-mcp-config", "--", "go"}},
		{"effort and Chrome", claudeSession{effort: "high", chrome: []string{"--no-chrome"}},
			[]string{"--append-system-prompt-file", "/prompt.md", "--effort", "high", "--strict-mcp-config",
				"--no-chrome", "--", "go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.s.repo = t.TempDir()
			got, err := tc.s.args("/prompt.md", "--", "go")
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
