package claude

import (
	"encoding/json"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// An earlier worker left running in its tab beside the worker started since in the same worktree, as
// one renamed when its ticket is taken up again, has hooks that write to the same folder: what they
// report is not of the worker the folder's records are now of, and is left out of them.
func TestEarlierSessionLeftRunningIsNotRecorded(t *testing.T) {
	t.Parallel()
	const earlier, later = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	start := func(id, source string) string {
		return `{"hook_event_name":"SessionStart","session_id":"` + id + `",` +
			`"transcript_path":"/c/projects/x/` + id + `.jsonl","source":"` + source + `"}`
	}
	for _, c := range []struct {
		name, event, input string
		want               dispatch.AgentState
	}{
		{"the earlier one exits", "SessionEnd",
			`{"hook_event_name":"SessionEnd","session_id":"` + earlier + `","reason":"prompt_input_exit"}`, dispatch.StateWorking},
		{"the earlier one waits at its prompt", "Notification", `{"hook_event_name":"Notification","session_id":"` +
			earlier + `","notification_type":"idle_prompt"}`, dispatch.StateWorking},
		{"the earlier one compacts", "SessionStart", start(earlier, "compact"), dispatch.StateWorking},
		{"the earlier one's turn ends", "Stop", `{"hook_event_name":"Stop","session_id":"` + earlier + `"}`,
			dispatch.StateWorking},
		{"the later one exits", "SessionEnd",
			`{"hook_event_name":"SessionEnd","session_id":"` + later + `","reason":"prompt_input_exit"}`, dispatch.StateGone},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			wt := t.TempDir()
			records := Records(wt, "")
			settings := reportingSettings(t, wt, false)
			runHooks(t, settings, "SessionStart", start(earlier, "startup"))
			runHooks(t, settings, "Stop", `{"hook_event_name":"Stop","session_id":"`+earlier+`"}`)
			settings = reportingSettings(t, wt, false) // the later worker, launched in the same worktree
			runHooks(t, settings, "SessionStart", start(later, "startup"))
			runHooks(t, settings, "UserPromptSubmit", `{"hook_event_name":"UserPromptSubmit","session_id":"`+later+`"}`)
			runHooks(t, settings, c.event, c.input)
			if got := State(records); got != c.want {
				t.Errorf("%s: %s, want %s", c.name, got, c.want)
			}
			b, _, err := readRun(wt, sessionName)
			var rec hookInput
			if err != nil || json.Unmarshal(b, &rec) != nil || rec.Session != later {
				t.Errorf("%s: the session record is %q (%v), want the later one's", c.name, b, err)
			}
		})
	}
}
