package tmux

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A worker whose hooks tell a permission prompt asked is blocked while its screen shows the dialog,
// or can't be read; once it is allowed, which runs no hook until the tool is done, its screen shows
// none and it works, however long the tool runs.
func TestStatusOfAPermissionPromptAllowed(t *testing.T) {
	asked := func() string {
		return records(t, "events", `{"hook_event_name":"SessionStart"}`+"\n",
			"turn.json", `{"hook_event_name":"UserPromptSubmit"}`,
			"activity.json", `{"hook_event_name":"PermissionRequest","tool_name":"Bash"}`,
			"notice.json", `{"hook_event_name":"Notification","notification_type":"permission_prompt"}`)
	}
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
		f := newFakeTmux(t)
		f.listPanes(paneLine{id: "%5", agent: "x-1", kind: "claude", records: asked(), command: "2.1.0"})
		f.answer("capture-pane", c.screen)
		if c.fails {
			f.fail("capture-pane", "can't find pane: %5")
		}
		if st, err := (Terminal{}).Status(bg, "x-1"); st != c.want || err != nil {
			t.Errorf("%s: %q %v, want %q", c.name, st, err, c.want)
		}
		if got := f.call("capture-pane"); !slices.Equal(got, []string{"capture-pane", "-p", "-J", "-t", "%5"}) {
			t.Errorf("%s: read the screen with %q, want the visible screen", c.name, got)
		}
	}
}

// A worker allowed to run a command that takes long works while it runs, though no hook ran since
// it asked: the fake Claude Code runs its hooks in Claude Code's order (PreToolUse, PermissionRequest,
// then PostToolUse once the command is done) and clears the dialog as the command runs.
func TestLivePermissionAllowedWorks(t *testing.T) {
	l := newLive(t)
	done := filepath.Join(t.TempDir(), "done")
	l.script(false, "start", "prompt", "permission while [ ! -e '"+done+"' ]; do sleep 0.05; done", "stop", "idle")
	_, pane := l.tab("x-1")
	if err := l.term.StartAgent(bg, "x-1", "claude", pane, l.hooks()); err != nil {
		t.Fatal(err)
	}
	if err := l.term.Prompt(bg, "x-1", "Work on x-1."); err != nil {
		t.Fatal(err)
	}
	l.eventually("never blocked on its permission prompt", func() bool { return l.status("x-1") == dispatch.StateBlocked })
	if err := l.term.SendKeys(bg, "x-1", "1", "enter"); err != nil {
		t.Fatal(err)
	}
	l.eventually("not working while its command allowed runs", func() bool { return l.status("x-1") == dispatch.StateWorking })
	if err := os.WriteFile(done, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !l.term.WaitReady(bg, "x-1") {
		t.Fatalf("not idle once its command was done: %s", l.status("x-1"))
	}
}
