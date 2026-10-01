package claude

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// runEditHook runs the hook that records edited paths, as Claude Code would before a tool use.
func runEditHook(t *testing.T, settings map[string]any, tool, input string) {
	t.Helper()
	for _, e := range settings["hooks"].(map[string]any)["PreToolUse"].([]any) {
		entry := e.(map[string]any)
		if !slices.Contains(strings.Split(entry["matcher"].(string), "|"), tool) {
			continue
		}
		command := entry["hooks"].([]any)[0].(map[string]any)["command"].(string)
		if strings.Contains(command, editsName) {
			cmd := exec.Command("sh", "-c", command)
			cmd.Stdin = strings.NewReader(input)
			if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 {
				t.Fatalf("%s hook: %v %q", tool, err, out)
			}
			return
		}
	}
	t.Fatalf("no edit hook for %s", tool)
}

func TestHooksRecordEveryFileTheWorkerEdits(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "it's a worktree")
	var r Reporter
	stale := filepath.Join(wt, ".orchestra", "run", editsName)
	os.MkdirAll(filepath.Dir(stale), 0o755)
	os.WriteFile(stale, []byte(filepath.Join(wt, "old.go")+"\n"), 0o644)

	args, err := r.ReportArgs(wt)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.EditedFiles(wt); got != nil {
		t.Errorf("an earlier worker's edits should be removed: %q", got)
	}
	var settings map[string]any
	b, _ := os.ReadFile(args[1])
	if err := json.Unmarshal(b, &settings); err != nil {
		t.Fatalf("settings: %v\n%s", err, b)
	}
	in := func(tool string, input map[string]any) string {
		b, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "tool_name": tool, "tool_input": input})
		return string(b)
	}
	runEditHook(t, settings, "Edit", in("Edit", map[string]any{"file_path": filepath.Join(wt, "internal/dispatch/run.go"), "old_string": "a", "new_string": "b"}))
	runEditHook(t, settings, "Write", in("Write", map[string]any{"content": `{"file_path": "/elsewhere/evil.go"}`, "file_path": filepath.Join(wt, "internal/dispatch/footprint.go")}))
	runEditHook(t, settings, "MultiEdit", in("MultiEdit", map[string]any{"file_path": filepath.Join(wt, "internal/dispatch/run.go"), "edits": []any{}}))
	runEditHook(t, settings, "NotebookEdit", in("NotebookEdit", map[string]any{"notebook_path": filepath.Join(wt, "notes/a b.ipynb"), "new_source": "x"}))
	runEditHook(t, settings, "Write", in("Write", map[string]any{"file_path": "/tmp/scratch.txt", "content": "x"}))
	runEditHook(t, settings, "Write", in("Write", map[string]any{"file_path": filepath.Join(wt, ".orchestra/run/prompt.md"), "content": "x"}))
	runEditHook(t, settings, "Edit", in("Edit", map[string]any{"old_string": "no path"}))

	want := []string{"internal/dispatch/run.go", "internal/dispatch/footprint.go", "notes/a b.ipynb"}
	if got := r.EditedFiles(wt); !slices.Equal(got, want) {
		t.Errorf("edited %q, want %q", got, want)
	}
}

func TestEditedFilesByRealPath(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	real, _ = filepath.EvalSymlinks(real)
	b := []byte(filepath.Join(real, "a.go") + "\n" + filepath.Join(link, "b.go") + "\nc.go\n\n" + filepath.Join(link, "..", "outside.go") + "\n")
	if got := editedFiles(b, link); !slices.Equal(got, []string{"a.go", "b.go", "c.go"}) {
		t.Errorf("got %q", got)
	}
}
