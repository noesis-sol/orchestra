package cmux

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
	output := "⏺ Update(internal/cmux/agent.go)\n" +
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
		})
	}
}
