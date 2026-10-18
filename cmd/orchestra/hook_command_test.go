package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 'orchestra hook' is the workers' hook: missing from the usage, and run by hand, without the cookie
// the hook's command sets, it says what it is, writes nothing and exits 0.
func TestHookCommandIsHidden(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	records := t.TempDir()
	stdout, stderr, err := runIn(t, dir, nil, "hook", "Stop", records)
	if err != nil || stdout != "" || !strings.Contains(stderr, "hook of a Claude Code worker") {
		t.Errorf("orchestra hook = %v; stdout %q, stderr %q", err, stdout, stderr)
	}
	if entries, _ := os.ReadDir(records); len(entries) > 0 {
		t.Errorf("wrote %v", entries)
	}
	_, usage, err := runIn(t, dir, nil, "-h")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(usage, "hook") {
		t.Errorf("orchestra -h names the hook:\n%s", usage)
	}

	if _, stderr, err := runIn(t, dir, map[string]string{"ORCHESTRA_HOOK": "1"}, "hook", "Stop", records); err != nil ||
		stderr != "" {
		t.Errorf("orchestra hook with the cookie = %v, %q", err, stderr)
	}
	if b, _ := os.ReadFile(filepath.Join(records, "turn.json")); string(b) != `{"v":1,"hook_event_name":"Stop"}` {
		t.Errorf("turn.json = %q", b)
	}
}
