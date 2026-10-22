package dispatch

import (
	"strings"
	"testing"
)

// ExitTool's meaning, as the run report gives it, names each terminal workers run in.
func TestExitToolMeaningNamesEachTerminal(t *testing.T) {
	t.Parallel()
	got := exitMeaning(ExitTool)
	for _, terminal := range []string{"Herdr", "tmux", "cmux"} {
		if !strings.Contains(got, terminal) {
			t.Errorf("exitMeaning(ExitTool) = %q, leaves out %s", got, terminal)
		}
	}
}

// Every code a run exits with has a meaning for the reviewer, ExitSetup's included.
func TestEveryExitCodeHasAMeaning(t *testing.T) {
	t.Parallel()
	for _, code := range []int{ExitOK, ExitSetup, ExitStuck, ExitTool, ExitDirty, ExitMerge, ExitEnvironment,
		ExitUsageLimit, ExitInterrupted} {
		if got := exitMeaning(code); got == "unknown" || got == "" {
			t.Errorf("exitMeaning(%d) = %q", code, got)
		}
	}
}
