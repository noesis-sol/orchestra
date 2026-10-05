package herdr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// What Herdr answers, on stderr with exit status 1, for a pane it doesn't have.
const paneNotFoundStderr = `{"error":{"code":"pane_not_found","message":"pane w2B:p95 not found"},"id":"cli:pane:close"}`

// SplitPane opens a pane to the right of the given one, in cwd and focused, and returns its ID.
func TestSplitPane(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	herdrScript(t, "#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+args+"'\n"+
		`echo '{"id":"cli:pane:split","result":{"pane":{"agent_status":"unknown","focused":true,"pane_id":"w2B:p95",`+
		`"tab_id":"w2B:t7R","workspace_id":"w2B"},"type":"pane_created"}}'`+"\n")
	pane, err := (Terminal{}).SplitPane(context.Background(), "w2B:p94", "/Users/m/My Projects/app")
	if pane != "w2B:p95" || err != nil {
		t.Errorf("split: %q %v", pane, err)
	}
	want := "pane\nsplit\nw2B:p94\n--direction\nright\n--cwd\n/Users/m/My Projects/app\n--focus\n"
	if got, _ := os.ReadFile(args); string(got) != want {
		t.Errorf("SplitPane ran herdr with:\n%s\nwant:\n%s", got, want)
	}

	// What Herdr answers when the pane to split is gone.
	failingHerdr(t, `{"error":{"code":"pane_not_found","message":"pane not found"},"id":"cli:pane:split"}`)
	var he *Error
	if pane, err := (Terminal{}).SplitPane(context.Background(), "w2B:p94", "/tmp"); pane != "" ||
		!errors.As(err, &he) || he.Code != PaneNotFound {
		t.Errorf("Herdr refusing the split: %q %#v", pane, err)
	}

	for _, out := range []string{`{"result":{"type":"pane_created","pane":{}}}`, "not json"} {
		herdrScript(t, "#!/bin/sh\necho '"+out+"'\n")
		if pane, err := (Terminal{}).SplitPane(context.Background(), "w2B:p94", "/tmp"); pane != "" || err == nil {
			t.Errorf("output %s: %q %v", out, pane, err)
		}
	}
}

// ClosePane runs 'herdr pane close <pane>'; a pane that is already gone is no error.
func TestClosePane(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	herdrScript(t, "#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+args+"'\n")
	if err := (Terminal{}).ClosePane(context.Background(), "w2B:p95"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(args); string(got) != "pane\nclose\nw2B:p95\n" {
		t.Errorf("herdr got %q", got)
	}

	failingHerdr(t, paneNotFoundStderr)
	if err := (Terminal{}).ClosePane(context.Background(), "w2B:p95"); err != nil {
		t.Errorf("a pane the user closed: %v", err)
	}

	failingHerdr(t, `{"error":{"code":"server_busy","message":"try again"}}`)
	if err := (Terminal{}).ClosePane(context.Background(), "w2B:p95"); !HasCode(err, "server_busy") {
		t.Errorf("Herdr busy: %v", err)
	}

	failingHerdr(t, "panic: not JSON")
	if err := (Terminal{}).ClosePane(context.Background(), "w2B:p95"); err == nil {
		t.Error("an unexplained failure should be an error")
	}
}

// PaneOpen tells a pane Herdr still has, whatever runs in it, from one that is gone.
func TestPaneOpen(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	herdrScript(t, "#!/bin/sh\necho \"$*\" > '"+args+"'\n"+
		`echo '{"id":"cli:pane:get","result":{"pane":{"agent_status":"unknown","cwd":"/Users/m/app","focused":true,`+
		`"pane_id":"w2B:p95","revision":0,"tab_id":"w2B:t7R","workspace_id":"w2B"},"type":"pane_info"}}'`+"\n")
	if open, err := (Terminal{}).PaneOpen(context.Background(), "w2B:p95"); !open || err != nil {
		t.Errorf("open pane: %v %v", open, err)
	}
	if got, _ := os.ReadFile(args); string(got) != "pane get w2B:p95\n" {
		t.Errorf("ran herdr %s", got)
	}

	failingHerdr(t, `{"error":{"code":"pane_not_found","message":"pane w2B:p95 not found"},"id":"cli:pane:get"}`)
	if open, err := (Terminal{}).PaneOpen(context.Background(), "w2B:p95"); open || err != nil {
		t.Errorf("no such pane: %v %v", open, err)
	}

	failingHerdr(t, `{"error":{"code":"server_busy","message":"try again"}}`)
	if open, err := (Terminal{}).PaneOpen(context.Background(), "w2B:p95"); open || !HasCode(err, "server_busy") {
		t.Errorf("Herdr busy: %v %v", open, err)
	}

	herdrScript(t, "#!/bin/sh\necho '{\"result\":{}}'\n")
	if open, err := (Terminal{}).PaneOpen(context.Background(), "w2B:p95"); open || err == nil {
		t.Errorf("unexpected output: %v %v", open, err)
	}
}
