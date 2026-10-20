package agentio

import (
	"testing"
	"time"
)

// Reporter names who wrote a record: the tool, the session, the agent and the transcript, each said
// when the record has none.
func TestToolUseReporter(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 7, 9, 24, 2, 0, time.Local)
	for _, c := range []struct {
		u    ToolUse
		want string
	}{
		{ToolUse{Event: "PreToolUse", Tool: "Bash", At: at, Session: "s", Transcript: "/t/s.jsonl"},
			"written at 09:24:02 by tool Bash, session s, main agent, transcript /t/s.jsonl"},
		{ToolUse{Event: "PreToolUse", AgentType: "Explore"},
			"written at an unknown time by tool unnamed, session unnamed, agent with no ID (Explore), transcript unnamed"},
		{ToolUse{Event: "PreToolUse", Tool: "Read", Agent: "a1"},
			"written at an unknown time by tool Read, session unnamed, agent a1 (no type), transcript unnamed"},
	} {
		if got := c.u.Reporter(); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.u, got, c.want)
		}
	}
}

// A subagent counts as running while hooks report, by a note or a tool use of any agent, and no
// longer once none has for SubagentQuiet; with none started, none runs.
func TestSubagentsRunning(t *testing.T) {
	t.Parallel()
	now := time.Now()
	recent, old := now.Add(-time.Minute), now.Add(-SubagentQuiet)
	for _, c := range []struct {
		name        string
		rec         HookRecord
		used        time.Time
		wantRunning bool
	}{
		{"noted lately", HookRecord{Subagents: 1, Noted: recent}, old, true},
		{"a tool use lately", HookRecord{Subagents: 1, Noted: old}, recent, true},
		{"quiet", HookRecord{Subagents: 1, Noted: old}, old, false},
		{"nothing known", HookRecord{Subagents: 1}, time.Time{}, false},
		{"none started", HookRecord{Noted: now}, now, false},
	} {
		if got := c.rec.SubagentsRunning(c.used, now); got != c.wantRunning {
			t.Errorf("%s: running = %v, want %v", c.name, got, c.wantRunning)
		}
	}
}
