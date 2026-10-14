package claude

import "strings"

// A terminal reads a Claude session's visible screen where its hooks can't tell: for the dialogs that
// run no hook, and for a permission prompt allowed, which runs none either (see Hooks.Settle).

// dialogs are what Claude Code shows of the dialogs it asks before a session starts, which run no
// hook: whether to trust the folder, and whether to use the project's MCP servers.
var dialogs = []string{"trust this folder", "mcp server found in this project", "mcp servers found in this project"}

// ShowsDialog reports whether the bottom of screen, a Claude session's visible screen, shows one of
// the dialogs it asks before a session starts: whether to trust the folder, or to use the project's
// MCP servers.
func ShowsDialog(screen string) bool {
	for _, l := range bottom(screen) {
		for _, d := range dialogs {
			if strings.Contains(l, d) {
				return true
			}
		}
	}
	return false
}

// ShowsPermission reports whether the bottom of screen, a Claude session's visible screen, shows its
// permission dialog: the question "Do you want to proceed?" (or to make an edit, create a file, …)
// or the first of its answers, "1. Yes".
func ShowsPermission(screen string) bool {
	for _, l := range bottom(screen) {
		l = strings.TrimLeft(l, " \t│|❯>") // its frame, and the cursor on the answer chosen
		if strings.HasPrefix(l, "do you want to ") || strings.HasPrefix(l, "1. yes") {
			return true
		}
	}
	return false
}

// bottom is the last 25 lines of screen, in lower case: where Claude Code shows a dialog.
func bottom(screen string) []string {
	lines := strings.Split(strings.ToLower(screen), "\n")
	return lines[max(0, len(lines)-25):]
}
