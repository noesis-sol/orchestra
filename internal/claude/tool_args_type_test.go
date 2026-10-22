package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// An MCP tool's arguments are its own: one that isn't the string orchestra reads under its name (an
// array command, a numeric file_path) loses only itself, and the hook's input keeps the tool, the agent
// and the session, so the prompt reads as the subagent's own, which its stop answers.
func TestToolArgumentOfAnotherType(t *testing.T) {
	t.Parallel()
	ask := `{"hook_event_name":"PermissionRequest","session_id":"` + testID + `","agent_id":"a1",` +
		`"tool_name":"mcp__k8s__run","tool_input":{"command":["kubectl","get"],"file_path":42}}`
	u, ok := parseToolUse([]byte(ask))
	if !ok || u.Event != agentio.EventPermission || u.Tool != "mcp__k8s__run" || u.Agent != "a1" ||
		u.Session != testID || u.Command != "" {
		t.Errorf("parseToolUse = %+v %v", u, ok)
	}
	for _, in := range []string{`[1]`, `"x"`, `null`} {
		var h hookInput
		if err := json.Unmarshal([]byte(`{"tool_name":"T","tool_input":`+in+`}`), &h); err != nil || h.Tool != "T" {
			t.Errorf("tool_input %s: %+v %v", in, h, err)
		}
	}

	wt := t.TempDir()
	steps := []hookStep{stepSessionStart("startup"), stepPrompt, stepSubStart, stepAt("PermissionRequest", ask),
		stepNotification("permission_prompt")}
	playHooks(t, wt, steps)
	if h := ReadHooks(Records(wt, ""), ""); h.State != agentio.StateBlocked || !h.Asked {
		t.Errorf("at the prompt: ReadHooks = %s %v, want blocked, asked", h.State, h.Asked)
	}
	var r Reporter
	if u, ok := r.LastToolUse(wt); !ok || u.Event != agentio.EventPermission || u.Tool != "mcp__k8s__run" ||
		u.Agent != "a1" {
		t.Errorf("at the prompt: LastToolUse = %+v %v", u, ok)
	}
	if rec := r.HookRecord(wt); !slices.Equal(rec.Permissions, []agentio.Permission{{Tool: "mcp__k8s__run"}}) {
		t.Errorf("noted: %+v", rec.Permissions)
	}

	if _, err := os.Stat(filepath.Join(Records(wt, ""), askedName("a1", "mcp__k8s__run", []byte(ask)))); err != nil {
		t.Errorf("the subagent's prompt: %v", err)
	}

	wt = t.TempDir()
	playHooks(t, wt, append(steps, stepOtherBash, stepSubStop))
	if h := ReadHooks(Records(wt, ""), ""); h.State != agentio.StateWorking || h.Asked {
		t.Errorf("with the asking subagent stopped: ReadHooks = %s %v, want working, not asked", h.State, h.Asked)
	}
}
