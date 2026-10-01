// Package claude is Claude Code as orchestra's worker: hooks that have the worker record each tool
// it uses, and reading that record back. The hooks are loaded with --settings for the worker only,
// so the project's and the user's own settings are left as they are.
package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
)

const (
	settingsName = "hooks.json"    // in the worktree's .orchestra/run/
	activityName = "activity.json" // written by the hooks, read by orchestra
	editsName    = "edits"         // the paths the worker edits, one per line, appended by the hooks
)

// Reporter sets workers up to report what they are doing and reads what they reported.
type Reporter struct{}

// ReportArgs writes the reporting hooks to .orchestra/run/hooks.json in worktree and returns the
// arguments that load them. An earlier worker's records are removed, so nothing stale is read.
func (Reporter) ReportArgs(worktree string) ([]string, error) {
	dir := filepath.Join(worktree, project.Dir, project.RunName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	activity, edits := filepath.Join(dir, activityName), filepath.Join(dir, editsName)
	os.Remove(activity)
	os.Remove(edits)
	b, err := json.MarshalIndent(hookSettings(activity, edits), "", "  ")
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
// Before each edit, the file's path is also appended to edits: the record of the last tool use is
// replaced by the next one, and the scheduler needs every file the worker touches.
func hookSettings(activity, edits string) map[string]any {
	write := func(from string) string {
		return "f=" + command.ShellQuote(activity) + `; ` + from + ` > "$f.$$" && mv -f "$f.$$" "$f"`
	}
	event := func(name string) string {
		return "cat >/dev/null; " + write(`printf '{"hook_event_name":"`+name+`"}'`)
	}
	hook := func(command string) []any {
		return []any{map[string]any{"type": "command", "command": command}}
	}
	// The path is the first file_path (notebook_path for NotebookEdit) in the tool's input; a
	// quote inside the input's strings is escaped, so one in a Write's content can't match.
	edited := `grep -oE '"(file_path|notebook_path)" *: *"[^"]*"' | head -n 1 | sed -E 's/^[^:]*: *"//; s/"$//' >> ` +
		command.ShellQuote(edits)
	return map[string]any{"hooks": map[string]any{
		"PreToolUse": []any{
			map[string]any{"matcher": "*", "hooks": hook(write("cat"))},
			map[string]any{"matcher": "Edit|MultiEdit|Write|NotebookEdit", "hooks": hook(edited)},
		},
		"PostToolUse": []any{map[string]any{"matcher": "*", "hooks": hook(event("PostToolUse"))}},
		"Stop":        []any{map[string]any{"hooks": hook(event("Stop"))}},
	}}
}

// LastToolUse returns what the worker in worktree reported last, and when, and false when it has
// reported nothing readable.
func (Reporter) LastToolUse(worktree string) (dispatch.ToolUse, bool) {
	path := filepath.Join(worktree, project.Dir, project.RunName, activityName)
	b, err := os.ReadFile(path)
	if err != nil {
		return dispatch.ToolUse{}, false
	}
	u, ok := parseToolUse(b)
	if fi, err := os.Stat(path); ok && err == nil {
		u.At = fi.ModTime() // each report replaces the file, so it was written then
	}
	return u, ok
}

// EditedFiles returns the files the worker in worktree has edited, as paths in the repository, in
// the order it first edited them. Files outside the worktree and in .orchestra/run/ are left out.
func (Reporter) EditedFiles(worktree string) []string {
	b, err := os.ReadFile(filepath.Join(worktree, project.Dir, project.RunName, editsName))
	if err != nil {
		return nil
	}
	return editedFiles(b, worktree)
}

func editedFiles(b []byte, worktree string) []string {
	roots := []string{worktree}
	if real, err := filepath.EvalSymlinks(worktree); err == nil && real != worktree {
		roots = append(roots, real) // the worker may name it by its real path
	}
	seen := map[string]bool{}
	var files []string
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		p := strings.TrimSpace(sc.Text())
		if p == "" {
			continue
		}
		rel := ""
		for _, root := range roots {
			path := p
			if !filepath.IsAbs(path) {
				path = filepath.Join(worktree, path)
			}
			if r, err := filepath.Rel(root, path); err == nil && r != ".." && !strings.HasPrefix(r, ".."+string(filepath.Separator)) {
				rel = filepath.ToSlash(r)
				break
			}
		}
		if rel == "" || rel == "." || strings.HasPrefix(rel, project.Dir+"/"+project.RunName+"/") || seen[rel] {
			continue
		}
		seen[rel] = true
		files = append(files, rel)
	}
	return files
}

// parseToolUse reads one hook input: the event, and before a tool use the tool and its command.
func parseToolUse(b []byte) (dispatch.ToolUse, bool) {
	var in struct {
		Event string `json:"hook_event_name"`
		Tool  string `json:"tool_name"`
		Input struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if json.Unmarshal(b, &in) != nil || in.Event == "" {
		return dispatch.ToolUse{}, false
	}
	return dispatch.ToolUse{Event: in.Event, Tool: in.Tool, Command: in.Input.Command}, true
}
