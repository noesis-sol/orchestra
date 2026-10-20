package hookterm

import (
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// An agent kind with an AgentUI finds its records in the arguments it is launched with; one without
// has none, and its state is unknown, whatever its screen.
func TestUI(t *testing.T) {
	records := filepath.Join(t.TempDir(), "run")
	for _, tc := range []struct {
		kind    string
		reports bool
		args    []string
		records string
	}{
		{"claude", true, []string{"--settings", filepath.Join(records, "hooks.json")}, records},
		{"claude", true, []string{"--", "--settings", filepath.Join(records, "hooks.json")}, ""},
		{"claude", true, nil, ""},
		{"codex", false, []string{"--settings", filepath.Join(records, "hooks.json")}, ""},
		{"", false, nil, ""},
	} {
		if got := Reports(tc.kind); got != tc.reports {
			t.Errorf("Reports(%q) = %v, want %v", tc.kind, got, tc.reports)
		}
		ui := UI(tc.kind)
		if got, ok := ui.Records(tc.args); got != tc.records || ok != (tc.records != "") {
			t.Errorf("%q: records %q %v from %q, want %q", tc.kind, got, ok, tc.args, tc.records)
		}
		if tc.reports {
			continue
		}
		told, settle := ui.Read(records, "")
		screen := func() (string, error) { return "Do you trust this folder?", nil }
		if told != agentio.StateUnknown || settle(screen) != agentio.StateUnknown {
			t.Errorf("%q: told %s, settled %s; want unknown", tc.kind, told, settle(screen))
		}
	}
}
