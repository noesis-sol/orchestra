// Package claude is Claude Code as orchestra's worker: hooks that have the worker record each tool
// it uses, and reading that record back. The hooks are loaded with --settings for the worker only,
// so the project's and the user's own settings are left as they are.
package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/noesis-sol/orchestra/internal/command"
	"github.com/noesis-sol/orchestra/internal/dispatch"
	"github.com/noesis-sol/orchestra/internal/project"
	"golang.org/x/text/unicode/norm"
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
	root, err := project.OpenRun(worktree)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }() // nothing written is lost: WriteRun closed its file
	for _, name := range []string{activityName, editsName} {
		if err := project.RemoveRun(root, worktree, project.RunPath(name)); err != nil {
			return nil, err
		}
	}
	activity := filepath.Join(worktree, project.RunPath(activityName))
	edits := filepath.Join(worktree, project.RunPath(editsName))
	b, err := json.MarshalIndent(hookSettings(activity, edits), "", "  ")
	if err != nil {
		return nil, err
	}
	if err := project.WriteRun(root, worktree, project.RunPath(settingsName), b, 0o644); err != nil {
		return nil, err
	}
	path := filepath.Join(worktree, project.RunPath(settingsName))
	return []string{"--settings", path}, nil
}

// hookSettings records the hook's input before each tool use (the tool and its arguments), and
// only the event after a tool use (failed or not) and at the end of a turn: a finished tool's input
// carries its whole output. Each write goes through a temporary file, so a reader never sees half
// of one. Before each edit, the file's path is also appended to edits: the record of the last tool
// use is replaced by the next one, and the scheduler needs every file the worker touches.
//
// A reporting hook must never change what the worker does, and Claude Code takes a hook's exit
// status 2 as "block": the tool call is refused, or the turn may not end. dash, /bin/sh on Debian
// and Ubuntu, exits 2 when a redirection fails, as every write here does once the worker has
// removed .orchestra/run/. So each hook ignores its errors, reads all its input (a hook that
// stops early would leave Claude Code writing to a closed pipe) and exits 0. It doesn't make the
// folder again: a git stash pop of the worker's own git stash --all would then fail on the file.
func hookSettings(activity, edits string) map[string]any {
	write := func(from string) string {
		return "f=" + command.ShellQuote(activity) + `; ` + from + ` > "$f.$$" && mv -f "$f.$$" "$f"`
	}
	event := func(name string) string {
		return write(`printf '{"hook_event_name":"` + name + `"}'`)
	}
	hook := func(line string) []any {
		line = "{ " + line + "; cat >/dev/null; } 2>/dev/null; exit 0"
		return []any{map[string]any{"type": "command", "command": line}}
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
		// Claude Code runs PostToolUse after a tool that succeeded only; this is the other case.
		"PostToolUseFailure": []any{map[string]any{"matcher": "*", "hooks": hook(event("PostToolUse"))}},
		"Stop":               []any{map[string]any{"hooks": hook(event("Stop"))}},
	}}
}

// LastToolUse returns what the worker in worktree reported last, and when, and false when it has
// reported nothing readable.
func (Reporter) LastToolUse(worktree string) (dispatch.ToolUse, bool) {
	b, fi, err := readRun(worktree, activityName)
	if err != nil {
		return dispatch.ToolUse{}, false
	}
	u, ok := parseToolUse(b)
	if ok {
		u.At = fi.ModTime() // each report replaces the file, so it was written then
	}
	return u, ok
}

// EditedFiles returns the files the worker in worktree has edited, as paths in the repository, in
// the order it first edited them. Files outside the worktree and in .orchestra/run/ are left out.
func (Reporter) EditedFiles(worktree string) []string {
	b, _, err := readRun(worktree, editsName)
	if err != nil {
		return nil
	}
	return editedFiles(b, worktree)
}

// readRun reads the file name in worktree's .orchestra/run/ through an os.Root, which won't follow
// a symlink out of the worktree, and returns it with its details as it was read.
func readRun(worktree, name string) ([]byte, os.FileInfo, error) {
	root, err := os.OpenRoot(worktree)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = root.Close() }() // read-only: nothing to lose
	f, err := root.Open(project.RunPath(name))
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = f.Close() }() // read-only: nothing to lose
	fi, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	b, err := io.ReadAll(f)
	return b, fi, err
}

func editedFiles(b []byte, worktree string) []string {
	roots := []string{worktree}
	if resolved, err := filepath.EvalSymlinks(worktree); err == nil && resolved != worktree {
		roots = append(roots, resolved) // the worker may name it by its real path
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
			if r, ok := relative(root, path); ok {
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

// relative returns path relative to root, when path is in it. The two are compared in one Unicode
// form, NFC: a worker may name the worktree in the other form than orchestra does, as with é written
// as e and U+0301 (NFD, as Finder gives names). The part inside root is kept in the form path gives
// it; the dispatch maps it to the file as git lists it.
func relative(root, path string) (string, bool) {
	r, err := filepath.Rel(norm.NFC.String(root), norm.NFC.String(path))
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	if r == "." {
		return r, true
	}
	// NFC leaves the separators where they are, so r is as many of path's last parts as it has.
	sep := string(filepath.Separator)
	parts := strings.Split(filepath.Clean(path), sep)
	return strings.Join(parts[len(parts)-strings.Count(r, sep)-1:], sep), true
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
