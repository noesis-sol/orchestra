package herdr

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// SplitPaneAside opens a pane to the right of the given one, in cwd, leaving the focus where it is;
// FocusPane gives a pane the focus through the agent in it.
func TestSplitPaneAsideAndFocusPane(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	herdrScript(t, "#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+args+"'\n"+
		`echo '{"id":"cli:pane:split","result":{"pane":{"agent_status":"unknown","focused":false,"pane_id":"w2B:p95",`+
		`"tab_id":"w2B:t7R","workspace_id":"w2B"},"type":"pane_created"}}'`+"\n")
	pane, err := (Terminal{}).SplitPaneAside(context.Background(), "w2B:p94", "/repo")
	if pane != "w2B:p95" || err != nil {
		t.Errorf("split: %q %v", pane, err)
	}
	if got, _ := os.ReadFile(args); string(got) != "pane\nsplit\nw2B:p94\n--direction\nright\n--cwd\n/repo\n--no-focus\n" {
		t.Errorf("SplitPaneAside ran herdr with:\n%s", got)
	}

	if err := (Terminal{}).FocusPane(context.Background(), "w2B:p95"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(args); string(got) != "agent\nfocus\nw2B:p95\n" {
		t.Errorf("FocusPane ran herdr with:\n%s", got)
	}
	failingHerdr(t, paneNotFoundStderr)
	if err := (Terminal{}).FocusPane(context.Background(), "w2B:p95"); err == nil {
		t.Error("a pane that is gone: no error")
	}
}
