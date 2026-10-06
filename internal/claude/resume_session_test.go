package claude

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// A worker resuming the session of the one before it keeps that session's record until its own
// SessionStart hook writes it again: a start that fails after its hooks are set up (Herdr down,
// Ctrl+C) leaves the session for the next run to resume, and a resumed worker's hook names the
// session it resumed.
func TestResumedWorkerKeepsTheSessionUntilItStarts(t *testing.T) {
	wt := t.TempDir()
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
	p := transcriptAt(t, t.TempDir(), testID, `{"type":"user","message":{"role":"user","content":"Work on A."}}`)
	runHook(t, settings, "SessionStart", sessionInput(testID, p))

	// The resumed worker's start fails once its hooks are set up, before Claude Code runs them.
	if _, err := r.ResumeArgs(wt); err != nil {
		t.Fatal(err)
	}
	if s, ok := r.Session(wt); !ok || s.ID != testID || s.Transcript != p {
		t.Fatalf("Session after a failed resume = %+v, %v; want %s still there to resume", s, ok, testID)
	}

	// The next one starts: claude --resume keeps the session ID, and its hook writes the record again.
	args, err = r.ResumeArgs(wt)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(args[1])
	if err := json.Unmarshal(b, &settings); err != nil {
		t.Fatalf("settings: %v\n%s", err, b)
	}
	resumed := strings.Replace(sessionInput(testID, p), `"source":"startup"`, `"source":"resume"`, 1)
	runHook(t, settings, "SessionStart", resumed)
	if s, ok := r.Session(wt); !ok || s.ID != testID || s.Transcript != p {
		t.Errorf("Session after the resumed worker started = %+v, %v; want %s", s, ok, testID)
	}
	b, _ = os.ReadFile(wt + "/.orchestra/run/" + sessionName)
	if !strings.Contains(string(b), `"source":"resume"`) {
		t.Errorf("the record should be the resumed worker's: %s", b)
	}
}
