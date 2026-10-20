package agentkind

import "testing"

// Claude Code's screen is read with its rules; a kind with no screen reader of its own, or none, gets no
// reading, rather than Claude Code's: no input box holding the prompt, no latest action, no usage limit.
func TestOnlyAKindWithAScreenReaderReadsTheScreen(t *testing.T) {
	t.Parallel()
	screen := "❯ say hi to the ticket's worker, please\n" +
		"⏺ Bash(go test ./...)\n" +
		"  ⎿  You've hit your session limit · resets 7:41pm (Europe/Athens)\n"
	prompt := "say hi to the ticket's worker, please"
	if r := Claude.Reader(); !r.InputHolds(screen, prompt) || r.LastActivity(screen) != "⏺ Bash(go test ./...)" ||
		!r.ShowsUsageLimit(screen) {
		t.Errorf("Claude Code's screen read: input %v, activity %q, limit %v; want all read",
			r.InputHolds(screen, prompt), r.LastActivity(screen), r.ShowsUsageLimit(screen))
	}
	for _, k := range []Kind{Codex, Of("aider"), Named("")} {
		if k.Screen != nil {
			t.Errorf("%q has a screen reader, want none", k.Name)
		}
		if r := k.Reader(); r.InputHolds(screen, prompt) || r.LastActivity(screen) != "" || r.ShowsUsageLimit(screen) {
			t.Errorf("%q read the screen: input %v, activity %q, limit %v; want nothing", k.Name,
				r.InputHolds(screen, prompt), r.LastActivity(screen), r.ShowsUsageLimit(screen))
		}
	}
}
