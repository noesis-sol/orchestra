package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A PreToolUse record that replaces a worker's Stop, as an agent Claude Code forks at the end of a
// turn writes, still says when the turn ended; a prompt that starts the next turn clears it, and a
// new or resumed worker starts without an earlier one's.
func TestLastToolUseKeepsTheStopAToolUseReplaced(t *testing.T) {
	wt := t.TempDir()
	settings := reportingSettings(t, wt, false)
	var r Reporter

	runHooks(t, settings, "UserPromptSubmit", `{"hook_event_name":"UserPromptSubmit","prompt":"go"}`)
	runHooks(t, settings, "PreToolUse", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"bd show x"}}`)
	if u, ok := r.LastToolUse(wt); !ok || u.Event != "PreToolUse" || !u.Stopped.IsZero() {
		t.Errorf("mid-turn: %+v %v", u, ok)
	}
	runHooks(t, settings, "Stop", `{"hook_event_name":"Stop"}`)
	u, ok := r.LastToolUse(wt)
	if !ok || u.Event != "Stop" || time.Since(u.Stopped) > time.Minute || time.Until(u.Stopped) > time.Minute {
		t.Errorf("at the Stop: %+v %v", u, ok)
	}
	stopped := u.Stopped
	runHooks(t, settings, "PreToolUse", `{"hook_event_name":"PreToolUse","tool_name":"Read","agent_id":"a1","tool_input":{"file_path":"x"}}`)
	if u, ok := r.LastToolUse(wt); !ok || u.Event != "PreToolUse" || u.Tool != "Read" || !u.Stopped.Equal(stopped) {
		t.Errorf("a tool use after the Stop should keep it: %+v %v", u, ok)
	}
	runHooks(t, settings, "UserPromptSubmit", `{"hook_event_name":"UserPromptSubmit","prompt":"continue"}`)
	if u, ok := r.LastToolUse(wt); !ok || !u.Stopped.IsZero() {
		t.Errorf("a prompt should clear the Stop: %+v %v", u, ok)
	}

	runHooks(t, settings, "Stop", `{"hook_event_name":"Stop"}`)
	for _, resume := range []bool{false, true} {
		reportingSettings(t, wt, resume)
		if _, err := os.Stat(filepath.Join(wt, ".orchestra", "run", turnName)); !os.IsNotExist(err) {
			t.Errorf("resume %v: an earlier worker's turn record should be removed: %v", resume, err)
		}
	}
}
