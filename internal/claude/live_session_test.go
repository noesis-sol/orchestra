package claude

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLiveSession runs the real claude with the reporting hooks and reads back the session its
// SessionStart hook recorded, and the end of its transcript. Set CLAUDE_LIVE=1 to run it; it makes
// one small model call, and removes the transcript it leaves in ~/.claude/projects/.
func TestLiveSession(t *testing.T) {
	if os.Getenv("CLAUDE_LIVE") != "1" {
		t.Skip("set CLAUDE_LIVE=1 to call the real claude")
	}
	wt := t.TempDir()
	var r Reporter
	args, err := r.ReportArgs(wt)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	args = append([]string{"-p", "Run exactly this shell command and nothing else: echo resumable",
		"--allowedTools", "Bash", "--model", "haiku"}, args...)
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir, cmd.Stdin = wt, nil
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	s, ok := r.Session(wt)
	if !ok {
		b, _ := os.ReadFile(wt + "/.orchestra/run/" + sessionName)
		t.Fatalf("no session to resume; the record: %s", b)
	}
	t.Cleanup(func() {
		_ = os.Remove(s.Transcript)
		_ = os.Remove(filepath.Dir(s.Transcript)) // the worktree's folder there, if nothing else is in it
	})
	tail := r.TranscriptTail(wt)
	t.Logf("session %s, transcript %s:\n%s", s.ID, s.Transcript, tail)
	if !strings.Contains(tail, "tool Bash: echo resumable") || !strings.Contains(tail, "tool result: resumable") {
		t.Errorf("the transcript's end lacks the command and its result:\n%s", tail)
	}
}
