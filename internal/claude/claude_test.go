package claude

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// runHook runs the command of the event's hook in settings as Claude Code would, with input on stdin.
func runHook(t *testing.T, settings map[string]any, event, input string) {
	t.Helper()
	entry := settings["hooks"].(map[string]any)[event].([]any)[0].(map[string]any)
	command := entry["hooks"].([]any)[0].(map[string]any)["command"].(string)
	cmd := exec.Command("sh", "-c", command)
	cmd.Stdin = strings.NewReader(input)
	if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 {
		t.Fatalf("%s hook: %v %q", event, err, out)
	}
}

func TestHooksRecordWhatTheWorkerDoes(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "it's a worktree")
	var r Reporter
	stale := filepath.Join(wt, ".orchestra", "run", activityName)
	os.MkdirAll(filepath.Dir(stale), 0o755)
	os.WriteFile(stale, []byte(`{"hook_event_name":"PreToolUse","tool_name":"Edit"}`), 0o644)

	args, err := r.ReportArgs(wt)
	if err != nil || len(args) != 2 || args[0] != "--settings" {
		t.Fatalf("ReportArgs = %q, %v", args, err)
	}
	if _, ok := r.LastToolUse(wt); ok {
		t.Error("an earlier worker's record should be removed")
	}
	var settings map[string]any
	b, _ := os.ReadFile(args[1])
	if err := json.Unmarshal(b, &settings); err != nil {
		t.Fatalf("settings: %v\n%s", err, b)
	}

	runHook(t, settings, "PreToolUse", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"scripts/ci-local.sh 2>&1 | tail -25","description":"Running the full local CI"}}`)
	u, ok := r.LastToolUse(wt)
	if !ok || u.Event != "PreToolUse" || u.Tool != "Bash" || u.Command != "scripts/ci-local.sh 2>&1 | tail -25" {
		t.Errorf("before the tool: %+v %v", u, ok)
	}
	runHook(t, settings, "PostToolUse", `{"hook_event_name":"PostToolUse","tool_name":"Bash","tool_response":{"stdout":"`+strings.Repeat("x", 1<<16)+`"}}`)
	if u, ok := r.LastToolUse(wt); !ok || u.Event != "PostToolUse" || u.Tool != "" {
		t.Errorf("after the tool: %+v %v", u, ok)
	}
	runHook(t, settings, "Stop", `{"hook_event_name":"Stop"}`)
	if u, _ := r.LastToolUse(wt); u.Event != "Stop" || time.Since(u.At) > time.Minute || time.Until(u.At) > time.Minute {
		t.Errorf("at the end of the turn, reported just now: %+v", u)
	}
	if _, err := os.Stat(filepath.Join(wt, ".orchestra", "run")); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(filepath.Join(wt, ".orchestra", "run")); len(entries) != 2 {
		t.Errorf("temporary files left behind: %v", entries)
	}
}

func TestParseToolUse(t *testing.T) {
	if _, ok := parseToolUse([]byte("{half")); ok {
		t.Error("unreadable input should not parse")
	}
	if _, ok := parseToolUse([]byte(`{"tool_name":"Bash"}`)); ok {
		t.Error("input without an event should not parse")
	}
	u, ok := parseToolUse([]byte(`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"go test ./...","description":"Run the tests"}}`))
	if !ok || u.Tool != "Bash" || u.Command != "go test ./..." {
		t.Errorf("%+v %v", u, ok)
	}
}
