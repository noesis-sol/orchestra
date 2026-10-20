package claude

import (
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// A worker's own /resume ends its session with reason resume before Claude Code starts the one it
// resumes: a poll between the two finds a live worker, as after a /clear, never a gone one.
func TestOwnResumeIsNotGone(t *testing.T) {
	t.Parallel()
	turn := []hookStep{stepSessionStart("startup"), stepPrompt, stepUseBash, stepUsedIt, stepStop}
	for _, c := range []struct {
		name  string
		steps []hookStep
		want  agentio.State
	}{
		{"resuming, no new session yet", []hookStep{stepSessionEnd("resume")}, agentio.StateIdle},
		{"resumed", []hookStep{stepSessionEnd("resume"), stepSessionStart("resume")}, agentio.StateIdle},
		{"resumed, then prompted", []hookStep{stepSessionEnd("resume"), stepSessionStart("resume"), stepPrompt},
			agentio.StateWorking},
		{"resumed, then exited", []hookStep{stepSessionEnd("resume"), stepSessionStart("resume"),
			stepSessionEnd("prompt_input_exit")}, agentio.StateGone},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := playHooks(t, t.TempDir(), append(slices.Clip(turn), c.steps...)); got != c.want {
				t.Errorf("state after %s: %s, want %s", c.name, got, c.want)
			}
		})
	}
}
