package claude

import (
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// A permission prompt refused runs no hook: the last tool use stays the PermissionRequest, and only
// Claude Code's idle_prompt notice after it tells the worker sits at its prompt (Idle). A notice of the
// prompt itself, or one before the request, doesn't.
func TestLastToolUseTellsAPromptRefused(t *testing.T) {
	t.Parallel()
	then := func(steps ...hookStep) []hookStep {
		return slices.Concat([]hookStep{stepSessionStart("startup"), stepPrompt}, steps)
	}
	for _, c := range []struct {
		name  string
		steps []hookStep
		idle  bool
	}{
		{"asked", then(stepUseBash, stepAskBash), false},
		{"asked, notified", then(stepUseBash, stepAskBash, stepNotification("permission_prompt")), false},
		{"denied", then(stepUseBash, stepAskBash, stepNotification("idle_prompt")), true},
		{"denied, then asked again", then(stepUseBash, stepAskBash, stepNotification("idle_prompt"), stepPrompt,
			stepUseBash, stepAskBash), false},
		{"idle before it asked", then(stepNotification("idle_prompt"), stepPrompt, stepUseBash, stepAskBash), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			wt := t.TempDir()
			playHooks(t, wt, c.steps)
			u, ok := Reporter{}.LastToolUse(wt)
			if !ok || u.Event != agentio.EventPermission {
				t.Fatalf("LastToolUse = %+v %v, want the PermissionRequest", u, ok)
			}
			if idle := !u.Idle.IsZero(); idle != c.idle || idle && !u.Idle.After(u.At) {
				t.Errorf("Idle = %s with the request at %s, want idle %v", u.Idle, u.At, c.idle)
			}
		})
	}
}
