package claude

import (
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
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
		want  dispatch.AgentState
		asked bool
	}{
		{"the other call done", then(fetchStep("PostToolUse", allowed)), dispatch.StateBlocked, true},
		{"the other call failed", then(fetchStep("PostToolUseFailure", allowed)), dispatch.StateBlocked, true},
		{"allowed, both done", then(fetchStep("PostToolUse", allowed), fetchStep("PostToolUse", asking)),
			dispatch.StateWorking, false},
		{"allowed, done first", then(fetchStep("PostToolUse", asking)), dispatch.StateWorking, false},
		{"denied, told what to do", then(fetchStep("PostToolUse", allowed), stepPrompt), dispatch.StateWorking, false},
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

// The prompt's record matches its call's input however Claude Code writes it: the keys in another order,
// spaced, or none at all.
func TestAskedRecordOfTheInput(t *testing.T) {
	t.Parallel()
	ask := askedRecord("Read", []byte(`{"tool_name":"Read","tool_input":{"file_path":"/x","limit":5}}`))
	if got := askedRecord("Read", []byte(`{"tool_input": { "limit": 5, "file_path": "/x" }}`)); string(got) != string(ask) {
		t.Errorf("the same input reordered: %q, want %q", got, ask)
	}
	for _, other := range [][]byte{
		askedRecord("Read", []byte(`{"tool_input":{"file_path":"/y","limit":5}}`)),
		askedRecord("Grep", []byte(`{"tool_input":{"file_path":"/x","limit":5}}`)),
		askedRecord("Read", []byte(`{"tool_name":"Read"}`)),
	} {
		if string(other) == string(ask) {
			t.Errorf("another call's record %q is the prompt's", other)
		}
	}
	if string(askedRecord("Read", []byte("not JSON"))) != string(askedRecord("Read", nil)) {
		t.Error("an unreadable input is not read as none")
	}
}
