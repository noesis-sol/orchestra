package claude

import (
	"os"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// bashStep is the hook of the event for the main agent's Bash call of command.
func bashStep(event, command string) hookStep {
	return stepAt(event, `{"hook_event_name":"`+event+`","tool_name":"Bash","tool_input":{"command":"`+command+`"}}`)
}

// A permission prompt one call waits on is still asked when another call of the agent's, asked and
// allowed, ends, whichever of the two asked first.
func TestPermissionAskedBesideAnotherAskedCall(t *testing.T) {
	t.Parallel()
	const first, second = "go test ./...", "go vet ./..."
	start := []hookStep{stepSessionStart("startup"), stepPrompt, bashStep("PreToolUse", first),
		bashStep("PreToolUse", second)}
	for _, c := range []struct {
		name  string
		steps []hookStep
		want  agentio.State
		asked bool
	}{
		{"the other asked first", []hookStep{bashStep("PermissionRequest", first),
			bashStep("PermissionRequest", second), bashStep("PostToolUse", first)}, agentio.StateBlocked, true},
		{"the other asked last", []hookStep{bashStep("PermissionRequest", second),
			bashStep("PermissionRequest", first), bashStep("PostToolUse", first)}, agentio.StateBlocked, true},
		{"the other failed", []hookStep{bashStep("PermissionRequest", second),
			bashStep("PermissionRequest", first), bashStep("PostToolUseFailure", first)}, agentio.StateBlocked, true},
		{"both done", []hookStep{bashStep("PermissionRequest", second), bashStep("PermissionRequest", first),
			bashStep("PostToolUse", first), bashStep("PostToolUse", second)}, agentio.StateWorking, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			wt := t.TempDir()
			playHooks(t, wt, slices.Concat(start, c.steps))
			if h := ReadHooks(Records(wt, ""), ""); h.State != c.want || h.Asked != c.asked {
				t.Errorf("ReadHooks = %s %v, want %s %v", h.State, h.Asked, c.want, c.asked)
			}
		})
	}
}

// The end of a call, asked and allowed, run at once with the PermissionRequest of another call of the
// agent's, never removes that call's record.
func TestPermissionAskedAtOnceWithAnotherCallsEnd(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	call := func(event, command string) []byte {
		return []byte(`{"hook_event_name":"` + event + `","tool_name":"Bash","tool_input":{"command":"` + command + `"}}`)
	}
	for i := range 200 {
		first, second := "first "+strconv.Itoa(i), "second "+strconv.Itoa(i)
		writeRecords(root, "PermissionRequest", call("PermissionRequest", first), "")
		var wg sync.WaitGroup
		wg.Go(func() { writeRecords(root, "PostToolUse", call("PostToolUse", first), "") })
		wg.Go(func() { writeRecords(root, "PermissionRequest", call("PermissionRequest", second), "") })
		wg.Wait()
		if _, err := root.Stat(askedName("", "Bash", call("", second))); err != nil {
			t.Fatalf("round %d: the second call's prompt is no longer asked: %v", i, err)
		}
		if _, err := root.Stat(askedName("", "Bash", call("", first))); err == nil {
			t.Fatalf("round %d: the first call's prompt is still asked", i)
		}
		writeRecords(root, "PostToolUse", call("PostToolUse", second), "")
	}
}

// A subagent's stop removes the records of every prompt it asked, and no other agent's.
func TestSubagentStopRemovesItsAskedCalls(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	ask := func(agent, command string) []byte {
		return []byte(`{"hook_event_name":"PermissionRequest","agent_id":"` + agent +
			`","tool_name":"Bash","tool_input":{"command":"` + command + `"}}`)
	}
	for _, in := range [][]byte{ask("a1", "ls"), ask("a1", "pwd"), ask("a1-2", "ls"), ask("", "ls")} {
		writeRecords(root, "PermissionRequest", in, "")
	}
	writeRecords(root, "SubagentStop", []byte(`{"hook_event_name":"SubagentStop","agent_id":"a1"}`), "")
	names, err := askedRecords(root, ".")
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(names)
	want := []string{askedName("a1-2", "Bash", ask("a1-2", "ls")), askedName("", "Bash", ask("", "ls"))}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("asked after the stop = %q, want %q", names, want)
	}
}
