package cmux

import (
	"io"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/claude"
	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A worker started in the worktree of an earlier one left running in its own workspace, renamed, as
// when its deferred ticket is taken up again, works on while the earlier one exits: the earlier one's
// SessionEnd, which its hooks write to the same records, doesn't make the worker gone.
func TestWorkerWorksOnAsTheEarlierOneExits(t *testing.T) {
	const earlierWS, earlierSF = "00000000-0000-4000-8000-0000000000D0", "00000000-0000-4000-8000-0000000000D1"
	const earlier, later = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	wt := t.TempDir()
	records := claude.Records(wt, "")
	hook := func(event, session, fields string) {
		t.Helper()
		in := `{"hook_event_name":"` + event + `","session_id":"` + session + `"` + fields + `}`
		claude.Hook([]string{event, records}, func(string) string { return "1" }, strings.NewReader(in), io.Discard)
	}
	start := func() {
		t.Helper()
		if _, err := (claude.Reporter{}).ReportArgs(wt); err != nil {
			t.Fatal(err)
		}
	}
	start()
	hook("SessionStart", earlier, `,"source":"startup"`)
	hook("UserPromptSubmit", earlier, "")
	hook("Stop", earlier, "")
	start() // the later worker, its ticket taken up again
	hook("SessionStart", later, `,"source":"startup"`)
	hook("UserPromptSubmit", later, "")
	hook("PreToolUse", later, `,"tool_name":"Bash","tool_input":{"command":"go test ./..."}`)
	hook("SessionEnd", earlier, `,"reason":"prompt_input_exit"`) // /exit in the earlier one's workspace

	f := newFakeCmux(t)
	term := terminal(t)
	keep(t, term, book{Surfaces: map[string]surfaceEntry{
		earlierSF: {Workspace: earlierWS, Agent: "x-1", Kind: "claude", Records: records, Dir: wt},
		workSF:    {Workspace: workWS, Agent: "x", Kind: "claude", Records: records, Dir: wt}}})
	launchedPID(t, term, workSF, 7)
	f.answer("tree", treeJSON(ws{earlierWS, "x-1", []string{earlierSF}}, ws{workWS, "x", []string{workSF}}))
	f.answer("top", topJSON(workSF, 7))
	if st, err := term.Status(bg, "x"); st != dispatch.StateWorking || err != nil {
		t.Errorf("the worker as the earlier one exits: %s %v, want working", st, err)
	}
}
