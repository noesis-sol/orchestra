package claude

import (
	"strings"
	"testing"
)

// Claude Code starts its messages with ● where it doesn't run on macOS: there the limit's words, its notice
// that it continues by itself and a message after them read as they do with ⏺.
func TestShowsUsageLimitWithLinuxBullets(t *testing.T) {
	t.Parallel()
	linux := func(s string) string { return strings.ReplaceAll(s, "⏺", "●") }
	wentOn := screenStoppedToWait + "❯ Your claude.ai usage limit has reset. Continue the task you were working on " +
		"when the limit was reached; do not repeat work that is already complete.\n" +
		"⏺ Picking the ticket up where I left it.\n"
	for name, c := range map[string]struct {
		screen string
		want   bool
	}{
		"continuing by itself":            {screenAutoContinue, true},
		"asking what to do":               {screenLimitDialog, true},
		"stopped to wait":                 {screenStoppedToWait, true},
		"limited again":                   {screenLimitedAgain, true},
		"gone on after the limit":         {wentOn, false},
		"grace path, wrapping up":         {screenGraceWrappingUp, false},
		"grace path, waiting to go on":    {screenGraceStopped, true},
		"grace path, gone on after reset": {screenGraceContinued, false},
	} {
		if got := ShowsUsageLimit(linux(c.screen)); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
	if l := "● Usage limit reached · continuing automatically at 2:50pm · esc to cancel"; !showsContinue(l) {
		t.Errorf("showsContinue(%q) = false, want true", l)
	}
	if l := "● Usage limit reset · continuing automatically"; showsContinue(l) {
		t.Errorf("showsContinue(%q) = true, want false", l)
	}
}
