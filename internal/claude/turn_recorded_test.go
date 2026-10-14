package claude

import (
	"os"
	"path/filepath"
	"testing"
)

// A turn is recorded once a prompt or a turn's end is: not before any hook, nor once only the
// session started.
func TestReadHooksTellsATurn(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if ReadHooks(filepath.Join(dir, "none"), "").Turned || ReadHooks(dir, "").Turned {
		t.Error("no records tell a turn")
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(eventsName, `{"hook_event_name":"SessionStart","source":"startup"}`+"\n")
	if ReadHooks(dir, "").Turned {
		t.Error("a session started tells no turn")
	}
	write(turnName, "")
	if ReadHooks(dir, "").Turned {
		t.Error("an empty record tells no turn")
	}
	write(turnName, `{"hook_event_name":"UserPromptSubmit"}`)
	if !ReadHooks(dir, "").Turned {
		t.Error("a prompt is a turn")
	}
	write(turnName, `{"hook_event_name":"Stop"}`)
	if !ReadHooks(dir, "").Turned {
		t.Error("a turn's end is a turn")
	}
}
