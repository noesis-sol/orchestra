package claude

import (
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// fetchStep is the hook of the event for the main agent's WebFetch of url.
func fetchStep(event, url string) hookStep {
	return stepAt(event, `{"hook_event_name":"`+event+`","tool_name":"WebFetch",`+
		`"tool_input":{"url":"`+url+`","prompt":"the API"},"tool_use_id":"toolu_`+url[8:11]+`"}`)
}

// A permission prompt the agent waits on is still asked when another call of the same tool, which it
// runs beside it (Claude Code runs WebFetch, Read, Grep and read-only Bash in parallel), ends: until
// the asking call is done, or the turn moves on.
func TestPermissionAskedBesideTheSameTool(t *testing.T) {
	t.Parallel()
	const allowed, asking = "https://pkg.go.dev/os", "https://new-domain.example/x"
	then := func(steps ...hookStep) []hookStep {
		return slices.Concat([]hookStep{stepSessionStart("startup"), stepPrompt,
			fetchStep("PreToolUse", allowed), fetchStep("PreToolUse", asking),
			fetchStep("PermissionRequest", asking), stepNotification("permission_prompt")}, steps)
	}
	for _, c := range []struct {
		name  string
		steps []hookStep
		want  agentio.State
		asked bool
	}{
		{"the other call done", then(fetchStep("PostToolUse", allowed)), agentio.StateBlocked, true},
		{"the other call failed", then(fetchStep("PostToolUseFailure", allowed)), agentio.StateBlocked, true},
		{"allowed, both done", then(fetchStep("PostToolUse", allowed), fetchStep("PostToolUse", asking)),
			agentio.StateWorking, false},
		{"allowed, done first", then(fetchStep("PostToolUse", asking)), agentio.StateWorking, false},
		{"denied, told what to do", then(fetchStep("PostToolUse", allowed), stepPrompt), agentio.StateWorking, false},
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

// The prompt's record is named by its call's input however Claude Code writes it: the keys in another
// order, spaced, or none at all.
func TestAskedNameOfTheInput(t *testing.T) {
	t.Parallel()
	ask := askedName("", "Read", []byte(`{"tool_name":"Read","tool_input":{"file_path":"/x","limit":5}}`))
	if got := askedName("", "Read", []byte(`{"tool_input": { "limit": 5, "file_path": "/x" }}`)); got != ask {
		t.Errorf("the same input reordered: %q, want %q", got, ask)
	}
	for _, other := range []string{
		askedName("", "Read", []byte(`{"tool_input":{"file_path":"/y","limit":5}}`)),
		askedName("", "Grep", []byte(`{"tool_input":{"file_path":"/x","limit":5}}`)),
		askedName("", "Read", []byte(`{"tool_name":"Read"}`)),
		askedName("a1", "Read", []byte(`{"tool_input":{"file_path":"/x","limit":5}}`)),
	} {
		if other == ask {
			t.Errorf("another call's record %q is the prompt's", other)
		}
	}
	if askedName("", "Read", []byte("not JSON")) != askedName("", "Read", nil) {
		t.Error("an unreadable input is not read as none")
	}
}
