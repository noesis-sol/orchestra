package claudescreen

import "testing"

// Where Claude Code doesn't run on macOS it starts its tool calls with ● rather than ⏺.
func TestLastActivityWithLinuxBullets(t *testing.T) {
	t.Parallel()
	screen := "● Bash(go test ./...)\n  ⎿  ok\n\n────────────────\n❯ \n────────────────\n"
	if got := (Reader{}).LastActivity(screen); got != "● Bash(go test ./...)" {
		t.Errorf("got %q", got)
	}
}
