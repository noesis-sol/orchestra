package claude

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The hooks' record carries when they last noted something, as their lines carry no time: the
// events file's last write. Before any note it is zero.
func TestHookRecordNotedIsTheLastNotesTime(t *testing.T) {
	wt := t.TempDir()
	var r Reporter
	settings := reportingSettings(t, wt, false)
	if rec := r.HookRecord(wt); !rec.Noted.IsZero() {
		t.Errorf("noted at %v before any note", rec.Noted)
	}
	runHooks(t, settings, "SubagentStart", `{"hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"Explore"}`)
	runHooks(t, settings, "SubagentStop", `{"hook_event_name":"SubagentStop","agent_id":"a1","agent_type":"Explore"}`)
	stopped := time.Now().Add(-time.Hour).Truncate(time.Second)
	events := filepath.Join(wt, ".orchestra", "run", eventsName)
	if err := os.Chtimes(events, stopped, stopped); err != nil {
		t.Fatal(err)
	}
	if rec := r.HookRecord(wt); !rec.Noted.Equal(stopped) || rec.Subagents != 0 {
		t.Errorf("noted at %v with %d subagents, want %v and none", rec.Noted, rec.Subagents, stopped)
	}
}
