package tmux

import (
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// Claude Code's start-up dialogs come only before a session's first turn: an idle agent whose output
// shows their words, as a diff of this very file would, is idle; one at the dialog before its first
// turn is blocked.
func TestStatusLooksForADialogOnlyBeforeTheFirstTurn(t *testing.T) {
	trust := "Quick safety check: is this a project you created or one you trust?\n\n❯ 1. Yes, I trust this folder\n  2. No, exit\n"
	mcp := "New MCP server found in this project: github\n❯ 1. Use this and all future MCP servers\n"
	output := "⏺ Update(internal/tmux/tmux.go)\n" +
		`  + var dialogs = []string{"trust this folder", "mcp server found in this project"}` + "\n\n❯ \n"
	afterTurn := func() string {
		return records(t, "events", `{"hook_event_name":"SessionStart","source":"startup"}`+"\n",
			"turn.json", `{"hook_event_name":"Stop"}`)
	}
	for _, c := range []struct {
		name    string
		records string
		screen  string
		want    dispatch.AgentState
	}{
		{"its output shows the trust dialog's words", afterTurn(), output + trust, dispatch.StateIdle},
		{"its output shows the MCP dialog's words", afterTurn(), mcp + output, dispatch.StateIdle},
		{"the trust dialog before any hook", records(t), trust, dispatch.StateBlocked},
		{"the MCP dialog after its session started", idleRecords(t), mcp, dispatch.StateBlocked},
	} {
		f := newFakeTmux(t)
		f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude", command: "2.1.0", records: c.records})
		f.answer("capture-pane", c.screen)
		if st, err := (Terminal{}).Status(bg, "x-1"); st != c.want || err != nil {
			t.Errorf("%s: %q %v, want %q", c.name, st, err, c.want)
		}
	}
}
