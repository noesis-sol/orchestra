package claude

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/agentio"
)

// orchestra-rie6.3: what a worker used, from its sessions' transcripts.

// tokenSessions are the fixture sessions in testdata/tokens: the first, whose transcript has a message
// written over several lines, one from before since, Claude Code's own notice of an API error, and a
// last line cut off as it is written, with a subagent's transcript of its own; the second, whose
// transcript is missing; the third, after a /clear, whose transcript repeats a message of the first's.
func tokenSessions(t *testing.T) []agentio.Session {
	t.Helper()
	dir, err := filepath.Abs(filepath.Join("testdata", "tokens", "projects", "-w"))
	if err != nil {
		t.Fatal(err)
	}
	var sessions []agentio.Session
	for _, id := range []string{"11111111-2222-4333-8444-555555555555", "22222222-2222-4333-8444-555555555555",
		"33333333-2222-4333-8444-555555555555"} {
		sessions = append(sessions, agentio.Session{ID: id, Transcript: filepath.Join(dir, id+".jsonl")})
	}
	return sessions
}

// Each message is counted once, by its ID, however many lines it is written over and in however many
// transcripts, from since on; a subagent's are counted under its model, a line cut off or of no usage is
// skipped, and a missing transcript is left out, with an error that says so.
func TestTokensCountEachMessageOnceWithItsSubagents(t *testing.T) {
	sessions := tokenSessions(t)
	since := time.Date(2026, 10, 21, 9, 30, 0, 0, time.UTC)
	got, errs := Reporter{}.Tokens(sessions, since)
	want := agentio.Tokens{
		"claude-opus-5-5":  {Input: 3 + 2 + 1, Output: 100 + 50 + 10, CacheWrite: 1000 + 200, CacheRead: 5000 + 6000 + 4000},
		"claude-haiku-5-5": {Input: 4, Output: 20, CacheWrite: 300, CacheRead: 700},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tokens %v, want %v", got, want)
	}
	if len(errs) != 1 || !errors.Is(errs[0], fs.ErrNotExist) {
		t.Fatalf("errors %v, want the second session's transcript missing", errs)
	}
	var pe *fs.PathError
	if !errors.As(errs[0], &pe) || pe.Path != sessions[1].Transcript {
		t.Errorf("error %v, want one naming %s", errs[0], sessions[1].Transcript)
	}
}

// Nothing before since counts: a session resumed from an earlier run has only this run's messages
// counted.
func TestTokensCountOnlyFromSince(t *testing.T) {
	sessions := tokenSessions(t)
	got, errs := Reporter{}.Tokens(sessions[2:], time.Date(2026, 10, 21, 10, 6, 0, 0, time.UTC))
	want := agentio.Tokens{"claude-opus-5-5": {Input: 1, Output: 10, CacheRead: 4000}}
	if !reflect.DeepEqual(got, want) || len(errs) > 0 {
		t.Errorf("tokens %v, errors %v; want %v", got, errs, want)
	}
	if got, errs := (Reporter{}).Tokens(sessions[2:], time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)); got != nil || errs != nil {
		t.Errorf("tokens %v, errors %v after the last message, want none", got, errs)
	}
}

// A transcript in place of which there is no plain file isn't read.
func TestTokensReadOnlyPlainFiles(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "projects", "-w")
	id := "44444444-2222-4333-8444-555555555555"
	if err := os.MkdirAll(filepath.Join(dir, id+".jsonl"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, errs := Reporter{}.Tokens([]agentio.Session{{ID: id, Transcript: filepath.Join(dir, id+".jsonl")}}, time.Time{})
	if got != nil || len(errs) != 1 {
		t.Errorf("tokens %v, errors %v; want none, and an error", got, errs)
	}
}

// The hooks note each session the worker starts, clears into or resumes, which the hook record lists
// once each, oldest first; a note naming a transcript that isn't where Claude Code keeps the session's
// is left out.
func TestHookRecordListsTheSessionsStarted(t *testing.T) {
	wt := t.TempDir()
	settings := reportingSettings(t, wt, false)
	sessions := tokenSessions(t)
	start := func(s agentio.Session, source string) {
		runHooks(t, settings, "SessionStart", `{"hook_event_name":"SessionStart","session_id":"`+s.ID+
			`","transcript_path":"`+s.Transcript+`","source":"`+source+`"}`)
	}
	start(sessions[0], "startup")
	start(sessions[2], "clear")
	start(sessions[0], "resume")
	start(agentio.Session{ID: sessions[1].ID, Transcript: "/tmp/elsewhere/" + sessions[1].ID + ".jsonl"}, "clear")
	got := Reporter{}.HookRecord(wt).Sessions
	if want := []agentio.Session{sessions[0], sessions[2]}; !reflect.DeepEqual(got, want) {
		t.Errorf("sessions %v, want %v", got, want)
	}
}
