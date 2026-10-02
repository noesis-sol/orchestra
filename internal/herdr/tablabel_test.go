package herdr

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TabLabel reads the tab's label with 'herdr tab get'; a tab Herdr doesn't have is no error.
func TestTabLabel(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	herdrScript(t, "#!/bin/sh\necho \"$*\" > '"+args+"'\n"+
		`echo '{"id":"cli:tab:get","result":{"tab":{"agent_status":"idle","focused":false,"label":"kinieta-9g6",`+
		`"number":226,"pane_count":1,"tab_id":"w2B:t72","workspace_id":"w2B"},"type":"tab_info"}}'`+"\n")
	if label, open, err := (Terminal{}).TabLabel(context.Background(), "w2B:t72"); label != "kinieta-9g6" || !open || err != nil {
		t.Errorf("open tab: %q %v %v", label, open, err)
	}
	if got, _ := os.ReadFile(args); string(got) != "tab get w2B:t72\n" {
		t.Errorf("ran herdr %s", got)
	}

	// What Herdr answers for a tab it doesn't have, its workspace gone or not.
	failingHerdr(t, `{"error":{"code":"tab_not_found","message":"tab w2B:t72 not found"},"id":"cli:tab:get"}`)
	if label, open, err := (Terminal{}).TabLabel(context.Background(), "w2B:t72"); label != "" || open || err != nil {
		t.Errorf("no such tab: %q %v %v", label, open, err)
	}

	failingHerdr(t, `{"error":{"code":"server_busy","message":"try again"}}`)
	if _, open, err := (Terminal{}).TabLabel(context.Background(), "w2B:t72"); open || !HasCode(err, "server_busy") {
		t.Errorf("Herdr busy: %v %v", open, err)
	}

	herdrScript(t, "#!/bin/sh\necho '{\"result\":{}}'\n")
	if _, open, err := (Terminal{}).TabLabel(context.Background(), "w2B:t72"); open || err == nil {
		t.Errorf("unexpected output: %v %v", open, err)
	}
}
