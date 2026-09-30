package claude

import (
	"context"
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/noesis-sol/orchestra/internal/dispatch"
)

// TestLiveReports runs the real claude with the reporting hooks and watches what it reports while
// it works, as the loop does. Set CLAUDE_LIVE=1 to run it; it makes one small model call.
func TestLiveReports(t *testing.T) {
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
	args = append([]string{"-p", "Run exactly this shell command and nothing else: sleep 3; echo 'go test ./x'",
		"--allowedTools", "Bash", "--model", "haiku", "--no-session-persistence"}, args...)
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir, cmd.Stdin = wt, nil
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var seen []string
	for running := true; running; {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
			running = false
		case <-time.After(100 * time.Millisecond):
		}
		if u, ok := r.LastToolUse(wt); ok {
			label := u.Event + " " + dispatch.Doing(u, "sleep 3")
			if len(seen) == 0 || seen[len(seen)-1] != label {
				seen = append(seen, label)
			}
		}
	}
	t.Logf("reported: %q", seen)
	if !slices.Contains(seen, "PreToolUse testing") || seen[len(seen)-1] != "Stop " {
		t.Errorf("want the check command reported as testing, ending with Stop: %q", seen)
	}
}
