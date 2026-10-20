package claude

import (
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A permission prompt the records tell asked and not answered is asked, as Claude Code runs no hook
// as it is allowed: from its PreToolUse and PermissionRequest until the tool's PostToolUse, the
// terminal's screen must say whether its dialog still shows. A dialog of another kind is not.
func TestReadHooksTellsAPermissionPrompt(t *testing.T) {
	t.Parallel()
	then := func(steps ...hookStep) []hookStep {
		return slices.Concat([]hookStep{stepSessionStart("startup"), stepPrompt}, steps)
	}
	for _, c := range []struct {
		name  string
		steps []hookStep
		want  dispatch.AgentState
		asked bool
	}{
		{"asked", then(stepUseBash, stepAskBash), dispatch.StateBlocked, true},
		{"asked, notified", then(stepUseBash, stepAskBash, stepNotification("permission_prompt")),
			dispatch.StateBlocked, true},
		{"notified only", then(stepNotification("permission_prompt")), dispatch.StateBlocked, true},
		{"subagent asked", then(stepSubStart, stepSubBash, stepSubAsk), dispatch.StateBlocked, true},
		{"allowed, its tool done", then(stepUseBash, stepAskBash, stepUsedIt), dispatch.StateWorking, false},
		{"denied", then(stepUseBash, stepAskBash, stepNotification("idle_prompt")), dispatch.StateIdle, false},
		{"asking for input", then(stepNotification("elicitation_dialog")), dispatch.StateBlocked, false},
		{"using a tool", then(stepUseBash), dispatch.StateWorking, false},
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
	if h := ReadHooks(t.TempDir()+"/none", ""); h.State != dispatch.StateUnknown || h.Asked {
		t.Errorf("no records: %s %v", h.State, h.Asked)
	}
}
