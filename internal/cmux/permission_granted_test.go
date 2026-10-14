package cmux

import (
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A worker whose hooks tell a permission prompt asked is blocked while its screen shows the dialog,
// or can't be read; once it is allowed, which runs no hook until the tool is done, its screen shows
// none and it works, however long the tool runs.
func TestStatusOfAPermissionPromptAllowed(t *testing.T) {
	dialog := "● Bash(go test ./...)\nBash command\n  go test ./...\nDo you want to proceed?\n❯ 1. Yes\n  2. No\n"
	for _, c := range []struct {
		name, screen string
		fails        bool
		want         dispatch.AgentState
	}{
		{"at the dialog", dialog, false, dispatch.StateBlocked},
		{"allowed, its tool running", "● Bash(go test ./...)\n  Running…\n\n> \n", false, dispatch.StateWorking},
		{"screen not read", "", true, dispatch.StateBlocked},
		{"screen empty", "", false, dispatch.StateBlocked},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeCmux(t)
			term := terminal(t)
			asked := records(t, "events", `{"hook_event_name":"SessionStart"}`+"\n",
				"turn.json", `{"hook_event_name":"UserPromptSubmit"}`,
				"activity.json", `{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`,
				"notice.json", `{"hook_event_name":"Notification","notification_type":"permission_prompt"}`)
			keep(t, term, book{Surfaces: map[string]surfaceEntry{
				workSF: {Workspace: workWS, Agent: "x-1", Kind: "claude", Records: asked}}})
			launchedPID(t, term, workSF, 7)
			f.answer("tree", treeJSON(ws{workWS, "x-1", []string{workSF}}))
			f.answer("top", topJSON(workSF, 7))
			f.answer("read-screen", c.screen)
			if c.fails {
				f.fail("read-screen", "Error: not_found: Surface not found")
			}
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
