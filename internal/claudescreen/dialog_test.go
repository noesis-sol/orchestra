package claudescreen

import "testing"

// Claude Code's start-up dialogs show on screen, in any case; their words above the bottom of the
// screen don't count.
func TestShowsDialog(t *testing.T) {
	t.Parallel()
	for screen, want := range map[string]bool{
		"Quick safety check: is this a project you created or one you trust?\n\n❯ 1. Yes, I trust this folder\n": true,
		"New MCP server found in this project: github\n":                                                         true,
		"3 new MCP servers found in this project\n":                                                              true,
		"╭───╮\n│ Do you trust the files in this folder? │\n│ ❯ 1. Yes, Trust This Folder │\n╰───╯\n":            true,
		"Welcome to Claude Code\n\n> \n":                                                                         false,
		"":                                                                                                       false,
		"Yes, I trust this folder\n" + "\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\n\nok\n": false,
	} {
		if got := ShowsDialog(screen); got != want {
			t.Errorf("ShowsDialog(%q) = %v, want %v", screen, got, want)
		}
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
