package claude

import (
	"slices"
	"testing"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// A permission prompt the records tell asked and not answered is asked, as Claude Code runs no hook
// as it is allowed: from its PreToolUse and PermissionRequest until the tool's PostToolUse, the
// terminal's screen must say whether its dialog still shows. A dialog of another kind is not.
func TestStateAskedTellsAPermissionPrompt(t *testing.T) {
	t.Parallel()
	then := func(steps ...hookStep) []hookStep {
		return slices.Concat([]hookStep{stepSessionStart("startup"), stepPrompt}, steps)
	}
	for _, c := range []struct {
		name  string
		steps []hookStep
		want  dispatch.AgentState
		asked bool
	}{
		{"asked", then(stepUseBash, stepAskBash), dispatch.StateBlocked, true},
		{"asked, notified", then(stepUseBash, stepAskBash, stepNotification("permission_prompt")),
			dispatch.StateBlocked, true},
		{"notified only", then(stepNotification("permission_prompt")), dispatch.StateBlocked, true},
		{"subagent asked", then(stepSubStart, stepSubBash, stepSubAsk), dispatch.StateBlocked, true},
		{"allowed, its tool done", then(stepUseBash, stepAskBash, stepUsedIt), dispatch.StateWorking, false},
		{"denied", then(stepUseBash, stepAskBash, stepNotification("idle_prompt")), dispatch.StateIdle, false},
		{"asking for input", then(stepNotification("elicitation_dialog")), dispatch.StateBlocked, false},
		{"using a tool", then(stepUseBash), dispatch.StateWorking, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			wt := t.TempDir()
			playHooks(t, wt, c.steps)
			if st, asked := StateAsked(Records(wt, "")); st != c.want || asked != c.asked {
				t.Errorf("StateAsked = %s %v, want %s %v", st, asked, c.want, c.asked)
			}
		})
	}
	if st, asked := StateAsked(t.TempDir() + "/none"); st != dispatch.StateUnknown || asked {
		t.Errorf("no records: %s %v", st, asked)
	}
}

// Claude Code's permission dialogs show on screen, framed or not, whichever answer is chosen; a
// screen without one, as while the tool allowed runs, shows none.
func TestShowsPermission(t *testing.T) {
	t.Parallel()
	for screen, want := range map[string]bool{
		"● Bash(go test ./...)\n\nBash command\n  go test ./...\nDo you want to proceed?\n❯ 1. Yes\n  2. No\n": true,
		"╭────╮\n│ Edit file │\n│ Do you want to make this edit to main.go? │\n│ ❯ 1. Yes │\n│   2. Yes, allow all edits │\n" +
			"│   3. No, and tell Claude what to do differently (esc) │\n╰────╯\n": true,
		"Do you want to create notes.txt?\n  1. Yes\n❯ 2. No\n":                           true,
		"● Bash(go test ./...)\n  ⎿  Running…\n\n✻ Galloping… (esc to interrupt)\n\n> \n": false,
		"": false,
		"Do you want to proceed?\n" + "\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\nok\n": false,
	} {
		if got := ShowsPermission(screen); got != want {
			t.Errorf("ShowsPermission(%q) = %v, want %v", screen, got, want)
		}
	}
}
