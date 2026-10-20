package claude

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// What the hooks tell stands, but for what they can't: a dialog that runs no hook, which comes only
// before a session's first turn (an idle agent whose output shows its words, as a diff of this very
// file would, is idle), and a permission prompt allowed, which runs none either. A screen that can't
// be read, or reads empty, says nothing.
func TestSettle(t *testing.T) {
	t.Parallel()
	trust := "Quick safety check: is this a project you created or one you trust?\n\n❯ 1. Yes, I trust this folder\n  2. No, exit\n"
	mcp := "New MCP server found in this project: github\n❯ 1. Use this and all future MCP servers\n"
	output := "⏺ Update(internal/claude/screen.go)\n" +
		`  + var dialogs = []string{"trust this folder", "mcp server found in this project"}` + "\n\n❯ \n"
	permission := "● Bash(go test ./...)\nBash command\n  go test ./...\nDo you want to proceed?\n❯ 1. Yes\n  2. No\n"
	running := "● Bash(go test ./...)\n  Running…\n\n> \n"
	var (
		unknown   = Hooks{State: agentio.StateUnknown}
		started   = Hooks{State: agentio.StateIdle}
		turned    = Hooks{State: agentio.StateIdle, Turned: true}
		asked     = Hooks{State: agentio.StateBlocked, Asked: true, Turned: true}
		elicited  = Hooks{State: agentio.StateBlocked, Turned: true}
		working   = Hooks{State: agentio.StateWorking, Turned: true}
		gone      = Hooks{State: agentio.StateGone, Turned: true}
		firstTurn = Hooks{State: agentio.StateWorking}
	)
	for _, c := range []struct {
		name   string
		hooks  Hooks
		screen string
		read   bool
		want   agentio.State
	}{
		{"the trust dialog before any hook", unknown, trust, true, agentio.StateBlocked},
		{"the MCP dialog once its session started", started, mcp, true, agentio.StateBlocked},
		{"starting, no dialog", unknown, "Welcome to Claude Code\n", true, agentio.StateUnknown},
		{"idle at its prompt before its first turn", started, "❯ \n", true, agentio.StateIdle},
		{"a dialog's words in its output", turned, output + trust, true, agentio.StateIdle},
		{"the MCP dialog's words in its output", turned, mcp + output, true, agentio.StateIdle},
		{"a dialog, the screen not read", unknown, trust, false, agentio.StateUnknown},
		{"at the permission dialog", asked, permission, true, agentio.StateBlocked},
		{"allowed, its tool running", asked, running, true, agentio.StateWorking},
		{"asked, the screen not read", asked, running, false, agentio.StateBlocked},
		{"asked, the screen empty", asked, "", true, agentio.StateBlocked},
		{"asking for input", elicited, running, true, agentio.StateBlocked},
		{"working at its first turn", firstTurn, trust, true, agentio.StateWorking},
		{"working, a dialog's words in its output", working, output + trust, true, agentio.StateWorking},
		{"gone", gone, permission, true, agentio.StateGone},
	} {
		if got := settle(c.hooks, c.screen, c.read); got != c.want {
			t.Errorf("%s: settle = %s, want %s", c.name, got, c.want)
		}
	}
}

// Settle reads the screen only where the hooks can't tell, and takes one it can't read for none.
func TestSettleReadsTheScreenOnlyWhenNeeded(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		hooks Hooks
		reads bool
	}{
		{Hooks{State: agentio.StateUnknown}, true},
		{Hooks{State: agentio.StateIdle}, true},
		{Hooks{State: agentio.StateBlocked, Asked: true, Turned: true}, true},
		{Hooks{State: agentio.StateIdle, Turned: true}, false},
		{Hooks{State: agentio.StateWorking}, false},
		{Hooks{State: agentio.StateBlocked, Turned: true}, false},
		{Hooks{State: agentio.StateGone}, false},
	} {
		read := false
		got := c.hooks.Settle(func() (string, error) {
			read = true
			return "", errors.New("can't read it")
		})
		if read != c.reads || got != c.hooks.State {
			t.Errorf("%+v: read the screen %v, state %s; want %v, %s", c.hooks, read, got, c.reads, c.hooks.State)
		}
	}
	h := Hooks{State: agentio.StateBlocked, Asked: true, Turned: true}
	if got := h.Settle(func() (string, error) { return "> \n", nil }); got != agentio.StateWorking {
		t.Errorf("a permission prompt no longer shown: %s, want working", got)
	}
}

// ReadHooks reads the records of a session launched without hooks in its worktree's .orchestra/run/.
func TestReadHooksOfAWorktree(t *testing.T) {
	t.Parallel()
	wt := t.TempDir()
	if err := os.MkdirAll(Records(wt, ""), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(Records(wt, ""), name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(eventsName, `{"hook_event_name":"SessionStart","source":"startup"}`+"\n")
	write(turnName, `{"hook_event_name":"UserPromptSubmit"}`)
	if h := ReadHooks("", wt); h != (Hooks{State: agentio.StateWorking, Turned: true}) {
		t.Errorf("ReadHooks = %+v, want working, turned", h)
	}
	if h := ReadHooks(filepath.Join(t.TempDir(), "none"), wt); h != (Hooks{State: agentio.StateUnknown}) {
		t.Errorf("ReadHooks of a folder that isn't there = %+v, want unknown", h)
	}
}
