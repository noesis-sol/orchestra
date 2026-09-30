// Package claude is Claude Code as orchestra's worker: hooks that have the worker record each tool
// it uses, and reading that record back. The hooks are loaded with --settings for the worker only,
// so the project's and the user's own settings are left as they are.
package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

const (
	settingsName = "hooks.json"    // in the worktree's .orchestra/run/
	activityName = "activity.json" // written by the hooks, read by orchestra
)

// Reporter sets workers up to report what they are doing and reads what they reported.
type Reporter struct{}

// ReportArgs writes the reporting hooks to .orchestra/run/hooks.json in worktree and returns the
// arguments that load them. An earlier worker's record is removed, so nothing stale is read.
func (Reporter) ReportArgs(worktree string) ([]string, error) {
	dir := filepath.Join(worktree, project.Dir, project.RunName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	activity := filepath.Join(dir, activityName)
	os.Remove(activity)
	b, err := json.MarshalIndent(hookSettings(activity), "", "  ")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, settingsName)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return nil, err
	}
	return []string{"--settings", path}, nil
}

// hookSettings records the hook's input before each tool use (the tool and its arguments), and
// only the event after a tool use and at the end of a turn: a finished tool's input carries its
// whole output. Each write goes through a temporary file, so a reader never sees half of one.
func hookSettings(activity string) map[string]any {
	write := func(from string) string {
		return "f=" + shellQuote(activity) + `; ` + from + ` > "$f.$$" && mv -f "$f.$$" "$f"`
	}
	event := func(name string) string {
		return "cat >/dev/null; " + write(`printf '{"hook_event_name":"`+name+`"}'`)
	}
	hook := func(command string) []any {
		return []any{map[string]any{"type": "command", "command": command}}
	}
	return map[string]any{"hooks": map[string]any{
		"PreToolUse":  []any{map[string]any{"matcher": "*", "hooks": hook(write("cat"))}},
		"PostToolUse": []any{map[string]any{"matcher": "*", "hooks": hook(event("PostToolUse"))}},
		"Stop":        []any{map[string]any{"hooks": hook(event("Stop"))}},
	}}
}

// LastToolUse returns what the worker in worktree reported last, and false when it has reported
// nothing readable.
func (Reporter) LastToolUse(worktree string) (dispatch.ToolUse, bool) {
	path := filepath.Join(worktree, project.Dir, project.RunName, activityName)
	b, err := os.ReadFile(path)
	if err != nil {
		return dispatch.ToolUse{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return dispatch.ToolUse{}, false
	}
	u, ok := parseToolUse(b)
	u.At = info.ModTime()
	return u, ok
}

// parseToolUse reads one hook input: the event, and before a tool use the tool and its arguments.
func parseToolUse(b []byte) (dispatch.ToolUse, bool) {
	var in struct {
		Event string `json:"hook_event_name"`
		Tool  string `json:"tool_name"`
		Input struct {
			Command     string `json:"command"`
			Description string `json:"description"`
			FilePath    string `json:"file_path"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(b, &in) != nil || in.Event == "" {
		return dispatch.ToolUse{}, false
	}
	return dispatch.ToolUse{Event: in.Event, Tool: in.Tool, Command: in.Input.Command,
		Description: in.Input.Description, Path: in.Input.FilePath}, true
}

// shellQuote quotes s as one word for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
