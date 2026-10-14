package claude

import (
	"testing"
	"time"
)

// The hooks' record says when a subagent stopped only while its stop is their last note, and only of
// a subagent that started: Claude Code's forks at the end of a turn note a stop with no start (and
// no type), a moment after the worker's Stop.
func TestHookRecordSubagentStoppedIsARunningSubagentsLastStop(t *testing.T) {
	noted := time.Now().Add(-time.Minute)
	start := `{"hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"Explore"}` + "\n"
	stop := `{"hook_event_name":"SubagentStop","agent_id":"a1","agent_type":"Explore"}` + "\n"
	fork := `{"hook_event_name":"SubagentStop","agent_id":"af933ddf986912d45","agent_type":""}` + "\n"
	compact := `{"hook_event_name":"PreCompact","trigger":"auto"}` + "\n"
	for _, c := range []struct {
		name, events string
		stopped      bool
	}{
		{"a subagent's stop", start + stop, true},
		{"a fork's stop", fork, false},
		{"a fork's stop after a subagent's", start + stop + fork, false},
		{"a note after a subagent's stop", start + stop + compact, false},
		{"a subagent running", start, false},
	} {
		rec := parseEvents([]byte(c.events), noted)
		if want := c.stopped; !rec.SubagentStopped.IsZero() != want || want && !rec.SubagentStopped.Equal(noted) {
			t.Errorf("%s: subagent stopped at %v, want it at %v: %v", c.name, rec.SubagentStopped, noted, want)
		}
		if !rec.Noted.Equal(noted) {
			t.Errorf("%s: noted at %v, want %v", c.name, rec.Noted, noted)
		}
	}
}

// Read from the worktree, a subagent's stop is placed at the events file's last write.
func TestHookRecordSubagentStoppedFromTheHooks(t *testing.T) {
	wt := t.TempDir()
	var r Reporter
	settings := reportingSettings(t, wt, false)
	runHooks(t, settings, "SubagentStart", `{"hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"Explore"}`)
	if rec := r.HookRecord(wt); !rec.SubagentStopped.IsZero() {
		t.Errorf("subagent stopped at %v while it runs", rec.SubagentStopped)
	}
	runHooks(t, settings, "SubagentStop", `{"hook_event_name":"SubagentStop","agent_id":"a1","agent_type":"Explore"}`)
	if rec := r.HookRecord(wt); rec.SubagentStopped.IsZero() || !rec.SubagentStopped.Equal(rec.Noted) {
		t.Errorf("subagent stopped at %v, noted at %v; want the note's time", rec.SubagentStopped, rec.Noted)
	}
}
