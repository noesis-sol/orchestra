package command

import (
	"context"
	"testing"
)

func TestShellWordQuotesOnlyWhatAShellWouldSplit(t *testing.T) {
	for in, want := range map[string]string{
		"/repo-worktrees/k-1": "/repo-worktrees/k-1",
		"wt/k-1":              "wt/k-1",
		"/my repo/k-1":        "'/my repo/k-1'",
		"it's":                `'it'\''s'`,
		"":                    "''",
	} {
		got := ShellWord(in)
		if got != want {
			t.Errorf("ShellWord(%q) = %s, want %s", in, got, want)
		}
		out, err := Output(context.Background(), 0, "", "sh", "-c", "printf '%s' "+got)
		if err != nil || out != in {
			t.Errorf("ShellWord(%q) round-trips to %q (%v)", in, out, err)
		}
	}
}
