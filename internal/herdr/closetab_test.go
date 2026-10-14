package herdr

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// CloseTab runs 'herdr tab close <tab>'; a tab that is already gone is no error, as with ClosePane
// and the other terminals.
func TestCloseTab(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	herdrScript(t, "#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+args+"'\n")
	if err := (Terminal{}).CloseTab(context.Background(), "w2B:t7R"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(args); string(got) != "tab\nclose\nw2B:t7R\n" {
		t.Errorf("herdr got %q", got)
	}

	failingHerdr(t, `{"error":{"code":"tab_not_found","message":"tab w2B:t7R not found"},"id":"cli:tab:close"}`)
	if err := (Terminal{}).CloseTab(context.Background(), "w2B:t7R"); err != nil {
		t.Errorf("a tab the user closed: %v", err)
	}

	failingHerdr(t, `{"error":{"code":"server_busy","message":"try again"}}`)
	if err := (Terminal{}).CloseTab(context.Background(), "w2B:t7R"); !HasCode(err, "server_busy") {
		t.Errorf("Herdr busy: %v", err)
	}
}
