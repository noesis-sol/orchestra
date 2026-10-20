package claude

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

var (
	stepSubUsedIt = stepAt("PostToolUse", `{"hook_event_name":"PostToolUse","agent_id":"a1","agent_type":"verifier",`+
		`"tool_name":"Bash","tool_input":{"command":"rm -rf build"},"tool_response":{"stdout":"ok"}}`)
	stepOtherStart = stepAt("SubagentStart", `{"hook_event_name":"SubagentStart","agent_id":"b2","agent_type":"explorer"}`)
	stepOtherStop  = stepAt("SubagentStop", `{"hook_event_name":"SubagentStop","agent_id":"b2","agent_type":"explorer"}`)
	stepOtherBash  = stepAt("PreToolUse", `{"hook_event_name":"PreToolUse","agent_id":"b2","agent_type":"explorer",`+
		`"tool_name":"Bash","tool_input":{"command":"ls"}}`)
	stepOtherUsedIt = stepAt("PostToolUse", `{"hook_event_name":"PostToolUse","agent_id":"b2","agent_type":"explorer",`+
		`"tool_name":"Bash","tool_input":{"command":"ls"},"tool_response":{"stdout":"ok"}}`)
)

// A permission prompt one subagent waits on is still asked while another subagent works, whose tool
// uses replace the record of the last one: until the asking subagent's tool is done, it stops, or
// the turn moves on.
func TestPermissionAskedBesideAnotherSubagent(t *testing.T) {
	t.Parallel()
	then := func(steps ...hookStep) []hookStep {
		return slices.Concat([]hookStep{stepSessionStart("startup"), stepPrompt, stepSubStart, stepOtherStart}, steps)
	}
	asked := []hookStep{stepSubBash, stepSubAsk, stepNotification("permission_prompt")}
	for _, c := range []struct {
		name  string
		steps []hookStep
		want  agentio.State
		asked bool
	}{
		{"the other uses a tool", then(slices.Concat(asked, []hookStep{stepOtherBash})...), agentio.StateBlocked, true},
		{"the other's tool done", then(slices.Concat(asked, []hookStep{stepOtherBash, stepOtherUsedIt})...),
			agentio.StateBlocked, true},
		{"the other stopped", then(slices.Concat(asked, []hookStep{stepOtherBash, stepOtherUsedIt, stepOtherStop})...),
			agentio.StateBlocked, true},
		{"the session's own agent asked", then(stepUseBash, stepAskBash, stepOtherBash), agentio.StateBlocked, true},
		{"allowed, its tool done", then(slices.Concat(asked, []hookStep{stepOtherBash, stepSubUsedIt})...),
			agentio.StateWorking, false},
		{"the asking one stopped", then(slices.Concat(asked, []hookStep{stepOtherBash, stepSubStop})...),
			agentio.StateWorking, false},
		{"denied, told what to do", then(slices.Concat(asked, []hookStep{stepOtherBash, stepPrompt})...),
			agentio.StateWorking, false},
		{"resumed", then(slices.Concat(asked, []hookStep{stepOtherBash, stepResumed, stepSessionStart("resume")})...),
			agentio.StateIdle, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			wt := t.TempDir()
			playHooks(t, wt, c.steps)
			if h := ReadHooks(Records(wt, ""), ""); h.State != c.want || h.Asked != c.asked {
				t.Errorf("ReadHooks = %s %v, want %s %v", h.State, h.Asked, c.want, c.asked)
			}
		})
	}
}

// An agent's ID names its record only by the characters of a file's name: one with a path in it
// writes inside the records' folder all the same.
func TestPermissionAskedByAnAgentNamedAsAPath(t *testing.T) {
	t.Parallel()
	wt := t.TempDir()
	playHooks(t, wt, []hookStep{stepSessionStart("startup"), stepPrompt,
		stepAt("PermissionRequest", `{"hook_event_name":"PermissionRequest","agent_id":"../../x y","tool_name":"Bash"}`)})
	if h := ReadHooks(Records(wt, ""), ""); h.State != agentio.StateBlocked || !h.Asked {
		t.Errorf("ReadHooks = %s %v, want blocked, asked", h.State, h.Asked)
	}
	if _, err := os.Stat(filepath.Join(Records(wt, ""), askedPrefix+"xy")); err != nil {
		t.Errorf("the agent's record: %v", err)
	}
}
