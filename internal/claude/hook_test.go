package claude

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/command"
)

// temporaryFiles returns the files in the records folder that are none of the records nor the hooks'
// settings: a record's temporary file left behind.
func temporaryFiles(t *testing.T, records string) []string {
	t.Helper()
	entries, err := os.ReadDir(records)
	if err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		name := e.Name()
		if !slices.Contains(allRecords, name) && name != settingsName && !strings.HasPrefix(name, askedPrefix) ||
			strings.HasPrefix(name, askedPrefix) && strings.Contains(name, ".") {
			left = append(left, name)
		}
	}
	return left
}

// failingReader fails the test that reads it.
type failingReader struct{ t *testing.T }

func (r failingReader) Read([]byte) (int, error) {
	r.t.Error("the hook read its input")
	return 0, errors.New("read")
}

// The worker's settings run the binary that launched it, by its absolute path, as 'hook <event>
// <records>' with the cookie set, for every event they hook.
func TestHooksRunTheLaunchingBinary(t *testing.T) {
	wt := t.TempDir()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(bin) {
		t.Fatalf("os.Executable() = %q", bin)
	}
	hooks := reportingSettings(t, wt, false)["hooks"].(map[string]any)
	for event, entries := range hooks {
		for _, e := range entries.([]any) {
			for _, h := range e.(map[string]any)["hooks"].([]any) {
				want := hookCookie + "=1 " + command.ShellQuote(bin) + " hook " + event + " " +
					command.ShellQuote(Records(wt, ""))
				if got := h.(map[string]any)["command"].(string); !strings.Contains(got, want) {
					t.Errorf("%s hook: %s\nwant it to run %s", event, got, want)
				}
			}
		}
	}
}

// The hooks write each record as the shell hooks before them did, byte for byte, but for the notes'
// fields left empty, which are left out, and for a value with a quote or a backslash, which is noted
// as it is rather than empty.
func TestHookRecordsAreTheShellHooks(t *testing.T) {
	wt := t.TempDir()
	settings := reportingSettings(t, wt, false)
	records := Records(wt, "")
	read := func(name string) string {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(records, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	pre := `{"session_id":"s","hook_event_name":"PreToolUse","tool_name":"Edit","tool_input":{"file_path":"a.go"}}` + "\n"
	runHooks(t, settings, "PreToolUse", " "+pre)
	if got, want := read(activityName), `{"v":1,`+pre[1:]; got != want {
		t.Errorf("activity after PreToolUse = %q, want %q", got, want)
	}
	if got := read(editsName); got != "a.go\n" {
		t.Errorf("edits = %q", got)
	}
	ask := `{"hook_event_name":"PermissionRequest","agent_id":"a/1","tool_name":"Bash","tool_input":{"command":"x"}}`
	runHooks(t, settings, "PermissionRequest", ask)
	if got, want := read(activityName), `{"v":1,`+ask[1:]; got != want {
		t.Errorf("activity at the prompt = %q, want %q", got, want)
	}
	if got, want := read(askedPrefix+"a1"), string(askedRecord("Bash", []byte(ask))); got != want {
		t.Errorf("asked = %q, want %q", got, want)
	}
	runHooks(t, settings, "PostToolUseFailure", `{"hook_event_name":"PostToolUseFailure","agent_id":"a/1","tool_name":"Bash",`+
		`"tool_input":{ "command": "x" }}`)
	if got := read(activityName); got != `{"v":1,"hook_event_name":"PostToolUse"}` {
		t.Errorf("activity after the tool = %q", got)
	}
	if _, err := os.Stat(filepath.Join(records, askedPrefix+"a1")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the prompt answered is still asked: %v", err)
	}
	runHooks(t, settings, "UserPromptSubmit", `{"hook_event_name":"UserPromptSubmit","prompt":"go"}`)
	if got := read(turnName); got != `{"v":1,"hook_event_name":"UserPromptSubmit"}` {
		t.Errorf("turn after the prompt = %q", got)
	}
	runHooks(t, settings, "Stop", `{"hook_event_name":"Stop"}`)
	if got, want := read(activityName)+read(turnName), strings.Repeat(`{"v":1,"hook_event_name":"Stop"}`, 2); got != want {
		t.Errorf("activity and turn at the Stop = %q, want %q", got, want)
	}
	start := `{"hook_event_name":"SessionStart","session_id":"s","source":"startup"}`
	runHooks(t, settings, "SessionStart", start)
	if got, want := read(sessionName), `{"v":1,`+start[1:]; got != want {
		t.Errorf("session = %q, want %q", got, want)
	}
	notice := `{"hook_event_name":"Notification","notification_type":"idle_prompt"}`
	runHooks(t, settings, "Notification", notice)
	if got, want := read(noticeName), `{"v":1,`+notice[1:]; got != want {
		t.Errorf("notice = %q, want %q", got, want)
	}
	runHooks(t, settings, "PreCompact", `{"hook_event_name":"PreCompact","trigger":"auto"}`)
	runHooks(t, settings, "SubagentStart", `{"hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"Explore"}`)
	runHooks(t, settings, "SubagentStop", `{"hook_event_name":"SubagentStop","agent_id":"a1","agent_type":"Explore"}`)
	runHooks(t, settings, "SessionEnd", `{"hook_event_name":"SessionEnd","reason":"say \"exit\""}`)
	runHooks(t, settings, "PermissionRequest", `{"hook_event_name":"PermissionRequest"}`)
	want := strings.Join([]string{
		`{"v":1,"hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"x"}}`,
		`{"v":1,"hook_event_name":"SessionStart","source":"startup"}`,
		`{"v":1,"hook_event_name":"PreCompact","trigger":"auto"}`,
		`{"v":1,"hook_event_name":"SubagentStart","agent_id":"a1","agent_type":"Explore"}`,
		`{"v":1,"hook_event_name":"SubagentStop","agent_id":"a1","agent_type":"Explore"}`,
		`{"v":1,"hook_event_name":"SessionEnd","reason":"say \"exit\""}`,
		`{"v":1,"hook_event_name":"PermissionRequest"}`,
		"",
	}, "\n")
	if got := read(eventsName); got != want {
		t.Errorf("events =\n%s\nwant\n%s", got, want)
	}
	if left := temporaryFiles(t, records); len(left) > 0 {
		t.Errorf("temporary files left behind: %v", left)
	}
}

// Run by hand, without the cookie, the hook says what it is, reads nothing and writes nothing.
func TestHookWithoutTheCookie(t *testing.T) {
	records := t.TempDir()
	var stderr strings.Builder
	if code := Hook([]string{"Stop", records}, func(string) string { return "" }, failingReader{t}, &stderr); code != 0 {
		t.Errorf("Hook = %d", code)
	}
	if !strings.Contains(stderr.String(), "hook of a Claude Code worker") {
		t.Errorf("stderr = %q", stderr.String())
	}
	if entries, _ := os.ReadDir(records); len(entries) > 0 {
		t.Errorf("wrote %v", entries)
	}
}

// The hook exits 0 and says nothing whatever its input, its arguments or its folder: input that is no
// JSON object leaves the records that carry it unwritten, and writes those of the event alone.
func TestHookExitsZeroWhateverHappens(t *testing.T) {
	cookie := func(k string) string {
		if k == hookCookie {
			return "1"
		}
		return ""
	}
	records := t.TempDir()
	for _, args := range [][]string{nil, {"Stop"}, {"Stop", records, "1", "more"}, {"NoSuchEvent", records},
		{"Stop", filepath.Join(records, "gone")}} {
		var stderr strings.Builder
		if code := Hook(args, cookie, strings.NewReader("{}"), &stderr); code != 0 || stderr.Len() > 0 {
			t.Errorf("Hook(%q) = %d, %q", args, code, stderr.String())
		}
	}
	if _, err := os.Stat(filepath.Join(records, "gone")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the hook made its folder: %v", err)
	}
	if entries, _ := os.ReadDir(records); len(entries) > 0 {
		t.Errorf("wrote %v", entries)
	}
	for _, event := range []string{"PreToolUse", "PostToolUse", "PermissionRequest", "SessionStart", "Notification",
		"SubagentStop", "Stop"} {
		for _, in := range []string{"", "not JSON", `{"half":`, `["PreToolUse"]`, `{"tool_name":7}`} {
			var stderr strings.Builder
			if code := Hook([]string{event, records}, cookie, strings.NewReader(in), &stderr); code != 0 ||
				stderr.Len() > 0 {
				t.Errorf("%s hook on %q = %d, %q", event, in, code, stderr.String())
			}
		}
	}
	for _, name := range []string{sessionName, noticeName} { // the last input, the only JSON object
		if b, _ := os.ReadFile(filepath.Join(records, name)); string(b) != `{"v":1,"tool_name":7}` {
			t.Errorf("%s = %q", name, b)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(records, turnName)); string(b) != `{"v":1,"hook_event_name":"Stop"}` {
		t.Errorf("turn = %q", b)
	}
}

// A hook whose binary is gone, as a go run's once it ends, still exits 0, says nothing and reads all
// its input, under sh and dash.
func TestHookWithItsBinaryGone(t *testing.T) {
	records := t.TempDir()
	settings := hookSettings(filepath.Join(t.TempDir(), "orchestra"), records)
	shells := []string{"sh"}
	if _, err := exec.LookPath("dash"); err == nil {
		shells = append(shells, "dash")
	}
	input := `{"hook_event_name":"Stop","x":"` + strings.Repeat("x", 1<<20) + `"}`
	for _, e := range settings["hooks"].(map[string]any)["Stop"].([]any) {
		for _, h := range e.(map[string]any)["hooks"].([]any) {
			for _, sh := range shells {
				in := strings.NewReader(input)
				cmd := exec.Command(sh, "-c", h.(map[string]any)["command"].(string))
				cmd.Stdin = in
				if out, err := cmd.CombinedOutput(); err != nil || len(out) > 0 || in.Len() > 0 {
					t.Errorf("under %s: %v %q, %d bytes of input unread", sh, err, out, in.Len())
				}
			}
		}
	}
}
