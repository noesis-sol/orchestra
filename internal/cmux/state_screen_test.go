package cmux

import (
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Where its hooks can't tell (claude.Hooks.Settle), a worker's state reads its visible screen only,
// not the scrollback above it, whose output may well show a dialog's words: before its first turn,
// for a dialog that runs no hook, and on a permission prompt, which runs none as it is allowed.
func TestStatusReadsTheVisibleScreen(t *testing.T) {
	asked := func() string {
		return records(t, "events", `{"hook_event_name":"SessionStart"}`+"\n",
			"turn.json", `{"hook_event_name":"UserPromptSubmit"}`,
			"activity.json", `{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`,
			"notice.json", `{"hook_event_name":"Notification","notification_type":"permission_prompt"}`)
	}
	for _, c := range []struct {
		name, records, screen string
		want                  dispatch.AgentState
	}{
		{"the trust dialog before any hook", records(t), "❯ 1. Yes, I trust this folder\n", dispatch.StateBlocked},
		{"the MCP dialog before its first turn", idleRecords(t), "New MCP server found in this project: github\n",
			dispatch.StateBlocked},
		{"a permission prompt allowed", asked(), "● Bash(go test ./...)\n  Running…\n\n> \n", dispatch.StateWorking},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeCmux(t)
			term := terminal(t)
			keep(t, term, book{Surfaces: map[string]surfaceEntry{
				workSF: {Workspace: workWS, Agent: "x-1", Kind: "claude", Records: c.records}}})
			launchedPID(t, term, workSF, 7)
			f.answer("tree", treeJSON(ws{workWS, "x-1", []string{workSF}}))
			f.answer("top", topJSON(workSF, 7))
			f.answer("read-screen", c.screen)
			if st, err := term.Status(bg, "x-1"); st != c.want || err != nil {
				t.Errorf("got %s %v, want %s", st, err, c.want)
			}
			if got := cmdOf(f.call("read-screen")); !slices.Equal(got,
				[]string{"read-screen", "--workspace", workWS, "--surface", workSF}) {
				t.Errorf("read the screen with %q, want the visible screen", got)
			}
		})
	}
}
