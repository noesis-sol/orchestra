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
