package claude

import (
	"os"
	"path/filepath"
	"testing"
)

// A turn is recorded once a prompt or a turn's end is: not before any hook, nor once only the
// session started.
func TestTurnRecorded(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if TurnRecorded(filepath.Join(dir, "none")) || TurnRecorded(dir) {
		t.Error("no records tell a turn")
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(eventsName, `{"hook_event_name":"SessionStart","source":"startup"}`+"\n")
	if TurnRecorded(dir) {
		t.Error("a session started tells no turn")
	}
	write(turnName, "")
	if TurnRecorded(dir) {
		t.Error("an empty record tells no turn")
	}
	write(turnName, `{"hook_event_name":"UserPromptSubmit"}`)
	if !TurnRecorded(dir) {
		t.Error("a prompt is a turn")
	}
	write(turnName, `{"hook_event_name":"Stop"}`)
	if !TurnRecorded(dir) {
		t.Error("a turn's end is a turn")
	}
}
