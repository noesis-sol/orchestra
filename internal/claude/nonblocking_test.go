package claude

import (
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A reporting hook never changes what the worker does. With .orchestra/run/ removed, as git clean
// -fdx or git stash --all leave it, every hook still exits 0, says nothing and reads all its input,
// under sh and under dash (sh on Debian and Ubuntu), which exits 2, Claude Code's "block", when a
// redirection fails. The hooks don't make the folder again.
func TestHooksNeverBlockTheWorker(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "it's a worktree")
	if err := os.Mkdir(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	args, err := Reporter{}.ReportArgs(wt)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	b, _ := os.ReadFile(args[1])
	if err := json.Unmarshal(b, &settings); err != nil {
		t.Fatalf("settings: %v\n%s", err, b)
	}
	run := filepath.Join(wt, ".orchestra", "run")
	if err := os.RemoveAll(run); err != nil {
		t.Fatal(err)
	}
	shells := []string{"sh"}
	if _, err := exec.LookPath("dash"); err == nil {
		shells = append(shells, "dash")
	} else {
		t.Log("dash is not installed: the hooks run under sh only")
	}
	// Larger than a pipe's buffer: a hook that stopped reading would leave some of it unread.
	input := `{"hook_event_name":"PreToolUse","tool_name":"Write","tool_input":{"file_path":"x.go","content":"` +
		strings.Repeat("x", 1<<20) + `"}}`

	hooks := settings["hooks"].(map[string]any)
	n := 0
	for _, event := range slices.Sorted(maps.Keys(hooks)) {
		for _, e := range hooks[event].([]any) {
			for _, h := range e.(map[string]any)["hooks"].([]any) {
				command := h.(map[string]any)["command"].(string)
				n++
				for _, sh := range shells {
					in := strings.NewReader(input)
					cmd := exec.Command(sh, "-c", command)
					cmd.Stdin = in
					if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 || in.Len() > 0 {
						t.Errorf("%s hook under %s: %v %q, %d bytes of input unread\n%s",
							event, sh, err, out, in.Len(), command)
					}
				}
			}
		}
	}
	if n == 0 {
		t.Fatalf("no hooks in %s", b)
	}
	if _, err := os.Stat(run); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the hooks made .orchestra/run/ again: %v", err)
	}
}
