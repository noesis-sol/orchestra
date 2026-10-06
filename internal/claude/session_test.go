package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/noesis-sol/orchestra/internal/project"
)

const testID = "0b7c4a52-3f0e-4c5e-9a43-5d1f2c3b4a59"

// transcriptAt writes a transcript with lines for session id in a Claude Code config folder under
// dir, as Claude Code keeps it, and returns its path.
func transcriptAt(t *testing.T, dir, id string, lines ...string) string {
	t.Helper()
	p := filepath.Join(dir, "projects", "-wt", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// sessionInput is the SessionStart hook's input for session id with its transcript at p.
func sessionInput(id, p string) string {
	b, _ := json.Marshal(map[string]string{"hook_event_name": "SessionStart", "session_id": id,
		"transcript_path": p, "source": "startup"})
	return string(b)
}

// The SessionStart hook records the worker's session, which Session reads back while its transcript
// is there; a new worker's hooks remove the last one's record.
func TestSessionStartHookRecordsTheSession(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "it's a worktree")
	if err := os.Mkdir(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	var r Reporter
	args, err := r.ReportArgs(wt)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	b, _ := os.ReadFile(args[1])
	if err := json.Unmarshal(b, &settings); err != nil {
		t.Fatalf("settings: %v\n%s", err, b)
	}
	if _, ok := r.Session(wt); ok {
		t.Error("a session before the worker started")
	}
	p := transcriptAt(t, t.TempDir(), testID, `{"type":"user","message":{"role":"user","content":"Work on A."}}`)
	runHook(t, settings, "SessionStart", sessionInput(testID, p))
	if s, ok := r.Session(wt); !ok || s.ID != testID || s.Transcript != p {
		t.Errorf("Session = %+v, %v", s, ok)
	}
	if got := r.TranscriptTail(wt); got != "user: Work on A." {
		t.Errorf("TranscriptTail = %q", got)
	}
	if entries, _ := os.ReadDir(filepath.Join(wt, ".orchestra", "run")); len(entries) != 2 {
		t.Errorf("temporary files left behind: %v", entries)
	}
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Session(wt); ok {
		t.Error("a session whose transcript is gone can't be resumed")
	}
	if _, err := r.ReportArgs(wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(wt, project.RunPath(sessionName))); !os.IsNotExist(err) {
		t.Errorf("the last worker's session should be removed: %v", err)
	}
}

// A record is the worker's to change, so Session takes only a session ID, typed on a command line,
// and a transcript where Claude Code keeps it, named after the session, and a plain file.
func TestSessionRefusesWhatIsNoSession(t *testing.T) {
	dir := t.TempDir()
	good := transcriptAt(t, dir, testID, "{}")
	other := transcriptAt(t, dir, "1b7c4a52-3f0e-4c5e-9a43-5d1f2c3b4a59", "{}")
	link := filepath.Join(filepath.Dir(good), "2b7c4a52-3f0e-4c5e-9a43-5d1f2c3b4a59.jsonl")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(dir, "notes", "-wt", testID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(elsewhere), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(elsewhere, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, record string }{
		{"not JSON", "{half"},
		{"not a UUID", sessionInput("x; rm -rf ~", good)},
		{"another session's transcript", sessionInput(testID, other)},
		{"a relative path", sessionInput(testID, filepath.Join("projects", "-wt", testID+".jsonl"))},
		{"outside projects", sessionInput(testID, elsewhere)},
		{"a symlink", sessionInput("2b7c4a52-3f0e-4c5e-9a43-5d1f2c3b4a59", link)},
	} {
		t.Run(c.name, func(t *testing.T) {
			wt := t.TempDir()
			root, err := project.OpenRun(wt)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			if err := project.WriteRun(root, wt, project.RunPath(sessionName), []byte(c.record), 0o644); err != nil {
				t.Fatal(err)
			}
			var r Reporter
			if s, ok := r.Session(wt); ok {
				t.Errorf("Session = %+v", s)
			}
			if got := r.TranscriptTail(wt); got != "" {
				t.Errorf("TranscriptTail = %q", got)
			}
		})
	}
}

// The end of a transcript is its messages, tool calls and results, one line each, errors longer
// than other results; thinking, other entries and lines that aren't JSON are left out.
func TestTranscriptTail(t *testing.T) {
	long := strings.Repeat("é", 300)
	lines := []string{
		`{"type":"summary","summary":"Earlier work"}`,
		`{"type":"user","message":{"role":"user","content":"Work on A."}}`,
		`{"half`,
		`{"type":"assistant","message":{"role":"assistant","content":[` +
			`{"type":"thinking","thinking":"secret plan"},{"type":"text","text":"Running the tests.\n\nNow."},` +
			`{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"go test ./...","description":"Test"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1",` +
			`"content":"FAIL ./x\nexit status 1","is_error":true}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[` +
			`{"type":"tool_use","id":"t2","name":"Edit","input":{"file_path":"x.go","old_string":"a"}},` +
			`{"type":"tool_use","id":"t3","name":"TodoWrite","input":{"todos":[]}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2",` +
			`"content":[{"type":"text","text":"` + long + `"}]}]}}`,
		`{"type":"system","content":"API Error: overloaded","level":"error"}`,
	}
	want := strings.Join([]string{
		"user: Work on A.",
		"assistant: Running the tests. Now.",
		"tool Bash: go test ./...",
		"tool error: FAIL ./x exit status 1",
		"tool Edit: x.go",
		`tool TodoWrite: {"todos":[]}`,
		"tool result: " + strings.Repeat("é", resultChars/2) + "… (600 bytes)",
		"system: API Error: overloaded",
	}, "\n")
	if got := transcriptTail([]byte(strings.Join(lines, "\n"))); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}

	var many []string
	for i := range tailEntries + 5 {
		many = append(many, fmt.Sprintf(`{"type":"user","message":{"content":"message %d"}}`, i))
	}
	got := strings.Split(transcriptTail([]byte(strings.Join(many, "\n"))), "\n")
	if len(got) != tailEntries || got[0] != "user: message 5" || got[len(got)-1] != fmt.Sprintf("user: message %d", tailEntries+4) {
		t.Errorf("the last %d entries: got %d, %q … %q", tailEntries, len(got), got[0], got[len(got)-1])
	}
	big := strings.Repeat(`{"type":"user","message":{"content":"`+strings.Repeat("x", textChars)+`"}}`+"\n", tailEntries)
	if out := transcriptTail([]byte(big)); len(out) > tailChars || strings.HasPrefix(out, "x") {
		t.Errorf("cut to %d bytes from the start of a line: %d bytes, starting %q", tailChars, len(out), out[:20])
	}
}

// Only the end of a long transcript is read, from the start of a line.
func TestTranscriptTailReadsTheEnd(t *testing.T) {
	filler := `{"type":"user","message":{"content":"` + strings.Repeat("x", 1000) + `"}}`
	var lines []string
	for range tailBytes/len(filler) + 10 {
		lines = append(lines, filler)
	}
	lines = append(lines, `{"type":"assistant","message":{"content":[{"type":"text","text":"the end"}]}}`)
	p := transcriptAt(t, t.TempDir(), testID, lines...)
	wt := t.TempDir()
	root, err := project.OpenRun(wt)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := project.WriteRun(root, wt, project.RunPath(sessionName), []byte(sessionInput(testID, p)), 0o644); err != nil {
		t.Fatal(err)
	}
	got := Reporter{}.TranscriptTail(wt)
	if !strings.HasSuffix(got, "\nassistant: the end") || !strings.HasPrefix(got, "user: xxx") {
		t.Errorf("TranscriptTail = %.40q … %q", got, got[max(len(got)-40, 0):])
	}
}
