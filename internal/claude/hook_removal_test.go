package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// A permission prompt's note keeps the start of its command, and the removal in the whole command that
// Claude Code's safety check holds for a person, however far into it the rm is.
func TestPermissionNoteHasTheHeldRemoval(t *testing.T) {
	wt := t.TempDir()
	settings := reportingSettings(t, wt, false)
	scratch := "/private/tmp/claude-501/" + strings.Repeat("x", 80) + "/scratchpad"
	held := "S=" + scratch + "; cd $S && for f in a b; do ffmpeg -y -i $f.mov $f.mp4; done; rm -f $S/*.mp4"
	for _, cmd := range []string{held, "ffprobe -v error in.mp4"} {
		in, _ := json.Marshal(map[string]any{"hook_event_name": "PermissionRequest", "tool_name": "Bash",
			"tool_input": map[string]string{"command": cmd}})
		runHooks(t, settings, "PermissionRequest", string(in))
	}
	want := []agentio.Permission{
		{Tool: "Bash", Command: agentio.Clip(held, 80), Removal: "rm -f $S/*.mp4"},
		{Tool: "Bash", Command: "ffprobe -v error in.mp4"},
	}
	if got := (Reporter{}).HookRecord(wt).Permissions; !slices.Equal(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	b, err := os.ReadFile(filepath.Join(wt, ".orchestra", "run", eventsName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), scratch) {
		t.Errorf("a note should keep only the command's start:\n%s", b)
	}
}
