package claude

import (
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// UI is Claude Code as a hook-driven terminal knows it: its records where its --settings are, and its
// state as ReadHooks and Settle read it.
func TestUI(t *testing.T) {
	ui := UI{}
	records := filepath.Join(t.TempDir(), "run")
	if got, ok := ui.Records([]string{"--settings=" + filepath.Join(records, settingsName)}); got != records || !ok {
		t.Errorf("Records: %q %v, want %q", got, ok, records)
	}
	told, settle := ui.Read(records, "") // no hook ran yet
	for _, tc := range []struct {
		screen string
		want   dispatch.AgentState
	}{
		{"Do you trust this folder?", dispatch.StateBlocked},
		{"╭───╮\n│ > │\n╰───╯", dispatch.StateUnknown},
	} {
		if got := settle(func() (string, error) { return tc.screen, nil }); told != dispatch.StateUnknown || got != tc.want {
			t.Errorf("screen %q: told %s, settled %s; want unknown, %s", tc.screen, told, got, tc.want)
		}
	}
}
