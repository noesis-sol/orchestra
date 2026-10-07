package claude

import "testing"

// A tool use's record says who used the tool: the session, its transcript and the subagent, if any.
func TestParseToolUseReporter(t *testing.T) {
	u, ok := parseToolUse([]byte(`{"session_id":"s1","transcript_path":"/t/s1.jsonl","cwd":"/wt",` +
		`"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"/m/x.md","content":"x"},` +
		`"agent_id":"a7","agent_type":"general-purpose"}`))
	if !ok || u.Session != "s1" || u.Transcript != "/t/s1.jsonl" || u.Agent != "a7" || u.AgentType != "general-purpose" {
		t.Errorf("a subagent's tool use: %+v %v", u, ok)
	}
	u, ok = parseToolUse([]byte(`{"session_id":"s1","transcript_path":"/t/s1.jsonl","hook_event_name":"PreToolUse",` +
		`"tool_name":"Bash","tool_input":{"command":"bd close x"}}`))
	if !ok || u.Session != "s1" || u.Agent != "" || u.AgentType != "" || u.Command != "bd close x" {
		t.Errorf("the main agent's tool use: %+v %v", u, ok)
	}
}
