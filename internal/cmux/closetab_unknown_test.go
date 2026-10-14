package cmux

import (
	"slices"
	"strings"
	"testing"
)

// A workspace orchestra didn't open, when cmux can't list its workspaces, is left open, and the error
// names the workspace and says why, with cmux's own error.
func TestCloseTabWhenCmuxCannotSay(t *testing.T) {
	f := newFakeCmux(t)
	term := bookOf(t)
	f.serve()
	f.fail("tree", "Error: Command timed out")
	err := term.CloseTab(bg, userWS)
	if err == nil || !strings.Contains(err.Error(), userWS) || !strings.Contains(err.Error(), "Command timed out") {
		t.Errorf("cmux not answering: %v", err)
	}
	if slices.Contains(f.ops(), "close-workspace") {
		t.Errorf("cmux closed a workspace: %v", f.ops())
	}
}
