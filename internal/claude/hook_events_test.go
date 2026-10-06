package claude

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// runHooks runs every hook settings has for the event, as Claude Code would, each with input on stdin.
func runHooks(t *testing.T, settings map[string]any, event, input string) {
	t.Helper()
	for _, e := range settings["hooks"].(map[string]any)[event].([]any) {
		for _, h := range e.(map[string]any)["hooks"].([]any) {
			cmd := exec.Command("sh", "-c", h.(map[string]any)["command"].(string))
			cmd.Stdin = strings.NewReader(input)
			if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 {
				t.Fatalf("%s hook: %v %q", event, err, out)
			}
		}
	}
}

func reportingSettings(t *testing.T, wt string, resume bool) map[string]any {
	t.Helper()
	report := Reporter{}.ReportArgs
	if resume {
		report = Reporter{}.ResumeArgs
	}
	args, err := report(wt)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	b, _ := os.ReadFile(args[1])
	if err := json.Unmarshal(b, &settings); err != nil {
		t.Fatalf("settings: %v\n%s", err, b)
	}
	return settings
}

// The hooks record a permission prompt as the last report and note it, each compaction and each
// subagent's start and stop besides; a worker started or resumed afresh has none of them.
func TestHooksNotePermissionPromptsCompactionsAndSubagents(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "it's a worktree")
	if err := os.Mkdir(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	var r Reporter
	settings := reportingSettings(t, wt, false)

	runHooks(t, settings, "PreToolUse", `{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf build"}}`)
	runHooks(t, settings, "PermissionRequest", `{"session_id":"s","hook_event_name":"PermissionRequest","tool_name":"Bash",`+
		`"tool_input":{"command":"rm -rf build","description":"say \"tool_name\": \"Edit\""},"permission_suggestions":[]}`)
	u, ok := r.LastToolUse(wt)
	if !ok || u.Event != dispatch.EventPermission || u.Tool != "Bash" || u.Command != "rm -rf build" {
		t.Errorf("at the prompt: %+v %v", u, ok)
	}
	runHooks(t, settings, "PreCompact", `{"hook_event_name":"PreCompact","trigger":"auto","custom_instructions":""}`)
	runHooks(t, settings, "SubagentStart", `{"hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"Explore"}`)
	runHooks(t, settings, "SubagentStart", `{"hook_event_name":"SubagentStart","agent_id":"a2","agent_type":"general-purpose"}`)
	runHooks(t, settings, "SubagentStop", `{"hook_event_name":"SubagentStop","agent_id":"a1","agent_type":"Explore",`+
		`"last_assistant_message":"`+strings.Repeat("x", 1<<16)+`"}`)
	runHooks(t, settings, "PreCompact", `{"hook_event_name":"PreCompact","trigger":"manual","custom_instructions":"keep \"trigger\": \"x\""}`)
	if u, _ := r.LastToolUse(wt); u.Event != dispatch.EventPermission {
		t.Errorf("the notes should leave the last report as it was: %+v", u)
	}
	rec := r.HookRecord(wt)
	if !slices.Equal(rec.Permissions, []string{"Bash"}) || !slices.Equal(rec.Compactions, []string{"auto", "manual"}) ||
		rec.Subagents != 1 {
		t.Errorf("noted: %+v", rec)
	}
	runHooks(t, settings, "SubagentStop", `{"hook_event_name":"SubagentStop","agent_id":"a2","agent_type":"general-purpose"}`)
	if rec := r.HookRecord(wt); rec.Subagents != 0 {
		t.Errorf("with both subagents stopped: %+v", rec)
	}

	for _, resume := range []bool{true, false} {
		runHooks(t, reportingSettings(t, wt, resume), "SubagentStart",
			`{"hook_event_name":"SubagentStart","agent_id":"a3","agent_type":"Plan"}`)
		if rec := r.HookRecord(wt); len(rec.Permissions) != 0 || len(rec.Compactions) != 0 || rec.Subagents != 1 {
			t.Errorf("a worker started afresh (resumed: %v) should have only its own notes: %+v", resume, rec)
		}
	}
}

func TestParseEvents(t *testing.T) {
	b := []byte(`{"hook_event_name":"PermissionRequest","tool_name":"Edit"}
{half
{"hook_event_name":"PermissionRequest","tool_name":""}
{"hook_event_name":"PreCompact","trigger":""}
{"hook_event_name":"SubagentStop","agent_id":"never started","agent_type":"Explore"}
{"hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"Explore"}
{"hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"Explore"}
`)
	rec := parseEvents(b)
	if !slices.Equal(rec.Permissions, []string{"Edit", "an unnamed tool"}) ||
		!slices.Equal(rec.Compactions, []string{"unknown trigger"}) || rec.Subagents != 1 {
		t.Errorf("%+v", rec)
	}
	if rec := parseEvents(nil); rec.Permissions != nil || rec.Compactions != nil || rec.Subagents != 0 {
		t.Errorf("nothing noted: %+v", rec)
	}
}

func TestParsePermissionRequest(t *testing.T) {
	u, ok := parseToolUse([]byte(`{"hook_event_name":"PermissionRequest","tool_name":"Bash",` +
		`"tool_input":{"command":"go test ./..."},"permission_suggestions":[{"type":"addRules"}]}`))
	if !ok || u.Event != dispatch.EventPermission || u.Tool != "Bash" || u.Command != "go test ./..." {
		t.Errorf("%+v %v", u, ok)
	}
}
