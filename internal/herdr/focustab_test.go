package herdr

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// FocusTab runs 'herdr tab focus <tab>', and a tab Herdr doesn't know is its tab_not_found.
func TestFocusTabFocusesTheTab(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	herdrScript(t, "#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+args+"'\n")
	if err := (Terminal{}).FocusTab(context.Background(), "w2B:t72"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(args); string(got) != "tab\nfocus\nw2B:t72\n" {
		t.Errorf("herdr got %q", got)
	}

	failingHerdr(t, `{"error":{"code":"tab_not_found","message":"tab w2B:t9 not found"},"id":"cli:tab:focus"}`)
	if err := (Terminal{}).FocusTab(context.Background(), "w2B:t9"); !HasCode(err, TabNotFound) {
		t.Errorf("a missing tab: %v", err)
	}
}
