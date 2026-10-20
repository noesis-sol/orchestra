package tmux

import (
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/agentio"
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
		want                  agentio.State
	}{
		{"the trust dialog before any hook", records(t), "❯ 1. Yes, I trust this folder\n", agentio.StateBlocked},
		{"the MCP dialog before its first turn", idleRecords(t), "New MCP server found in this project: github\n",
			agentio.StateBlocked},
		{"a permission prompt allowed", asked(), "● Bash(go test ./...)\n  Running…\n\n> \n", agentio.StateWorking},
	} {
		f := newFakeTmux(t)
		f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude", command: "2.1.0", records: c.records})
		f.answer("capture-pane", c.screen)
		if st, err := (Terminal{}).Status(bg, "x-1"); st != c.want || err != nil {
			t.Errorf("%s: %q %v, want %q", c.name, st, err, c.want)
		}
		if got := f.call("capture-pane"); !slices.Equal(got, []string{"capture-pane", "-p", "-J", "-t", "%5"}) {
			t.Errorf("%s: read the screen with %q, want the visible screen", c.name, got)
		}
	}
}
